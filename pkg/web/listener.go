package web

import (
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type OriginValidator func(originHeader string) bool

type ListenerConfig struct {
	Logger            *log.Logger
	Addr              net.Addr
	Path              string
	Enabled           bool
	MaxMessageBytes   int64
	AcceptQueueSize   int
	IdleTimeout       time.Duration
	WriteTimeout      time.Duration
	PingInterval      time.Duration
	ValidateOrigin    OriginValidator
	RequiredProtocols []string
}

type Listener struct {
	cfg      ListenerConfig
	upgrader websocket.Upgrader
	sockets  chan net.Conn
	done     chan struct{}
	closed   int32

	mu      sync.Mutex
	conns   map[*conn]struct{}
	closeWg sync.WaitGroup
}

func NewListener(cfg ListenerConfig) *Listener {
	l := &Listener{
		cfg: cfg,
		upgrader: websocket.Upgrader{
			HandshakeTimeout: 20 * time.Second,
			Subprotocols:     cfg.RequiredProtocols,
			CheckOrigin: func(r *http.Request) bool {
				if cfg.ValidateOrigin == nil {
					return false
				}
				return cfg.ValidateOrigin(r.Header.Get("Origin"))
			},
		},
		sockets: make(chan net.Conn, cfg.AcceptQueueSize),
		done:    make(chan struct{}),
		conns:   make(map[*conn]struct{}),
	}
	return l
}

func (l *Listener) Accept() (net.Conn, error) {
	if atomic.LoadInt32(&l.closed) != 0 {
		return nil, net.ErrClosed
	}
	select {
	case conn := <-l.sockets:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *Listener) Close() error {
	if !atomic.CompareAndSwapInt32(&l.closed, 0, 1) {
		return net.ErrClosed
	}
	close(l.done)

	l.mu.Lock()
	conns := make([]*conn, 0, len(l.conns))
	for c := range l.conns {
		conns = append(conns, c)
	}
	l.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	l.closeWg.Wait()
	return nil
}

func (l *Listener) Addr() net.Addr {
	return l.cfg.Addr
}

func (l *Listener) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if atomic.LoadInt32(&l.closed) != 0 {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != l.cfg.Path {
		http.NotFound(w, r)
		return
	}
	if !l.cfg.Enabled {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	if !websocket.IsWebSocketUpgrade(r) {
		w.Header().Set("Upgrade", "websocket")
		http.Error(w, http.StatusText(http.StatusUpgradeRequired), http.StatusUpgradeRequired)
		return
	}

	protocol := negotiateSubprotocol(r.Header.Get("Sec-WebSocket-Protocol"), l.cfg.RequiredProtocols)
	if protocol == "" {
		http.Error(w, "websocket subprotocol negotiation failed", http.StatusBadRequest)
		return
	}

	ws, err := l.upgrader.Upgrade(w, r, nil)
	if err != nil {
		l.cfg.Logger.Printf("event=websocket_upgrade_failed remoteIp=%q err=%q", remoteHost(r.RemoteAddr), err.Error())
		return
	}
	if ws.Subprotocol() == "" {
		_ = ws.Close()
		return
	}
	conn := newConn(ws, l.cfg, func(c *conn) {
		l.mu.Lock()
		delete(l.conns, c)
		l.mu.Unlock()
		l.closeWg.Done()
	})

	l.mu.Lock()
	l.conns[conn] = struct{}{}
	l.closeWg.Add(1)
	l.mu.Unlock()

	select {
	case l.sockets <- conn:
	default:
		l.cfg.Logger.Printf("event=websocket_accept_queue_full remoteIp=%q capacity=%d", remoteHost(r.RemoteAddr), l.cfg.AcceptQueueSize)
		_ = conn.Close()
	}
}

type conn struct {
	ws           *websocket.Conn
	cfg          ListenerConfig
	closeOnce    sync.Once
	writeMu      sync.Mutex
	readMu       sync.Mutex
	reader       websocketReader
	onClose      func(*conn)
	pingStop     chan struct{}
}

type websocketReader struct {
	reader ioReader
}

type ioReader interface {
	Read([]byte) (int, error)
}

func newConn(ws *websocket.Conn, cfg ListenerConfig, onClose func(*conn)) *conn {
	c := &conn{
		ws:       ws,
		cfg:      cfg,
		onClose:  onClose,
		pingStop: make(chan struct{}),
	}
	ws.SetReadLimit(cfg.MaxMessageBytes)
	_ = ws.SetReadDeadline(time.Now().Add(cfg.IdleTimeout))
	ws.SetPongHandler(func(string) error {
		return ws.SetReadDeadline(time.Now().Add(cfg.IdleTimeout))
	})
	go c.pingLoop()
	return c
}

func (c *conn) Read(b []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	for {
		if c.reader.reader == nil {
			mt, r, err := c.ws.NextReader()
			if err != nil {
				return 0, err
			}
			if mt != websocket.BinaryMessage {
				_ = c.writeClose(websocket.CloseUnsupportedData, "binary frames required")
				return 0, errors.New("websocket text frames are not supported")
			}
			c.reader.reader = r
		}

		n, err := c.reader.reader.Read(b)
		if err == nil {
			return n, nil
		}
		if errors.Is(err, io.EOF) {
			c.reader.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		c.reader.reader = nil
		return n, err
	}
}

func (c *conn) Write(b []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.ws.SetWriteDeadline(time.Now().Add(c.cfg.WriteTimeout)); err != nil {
		return 0, err
	}
	if err := c.ws.WriteMessage(websocket.BinaryMessage, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (c *conn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.pingStop)
		err = c.ws.Close()
		if c.onClose != nil {
			c.onClose(c)
		}
	})
	return err
}

func (c *conn) LocalAddr() net.Addr  { return c.ws.LocalAddr() }
func (c *conn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }

func (c *conn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

func (c *conn) SetReadDeadline(t time.Time) error {
	return c.ws.SetReadDeadline(t)
}

func (c *conn) SetWriteDeadline(t time.Time) error {
	return c.ws.SetWriteDeadline(t)
}

func (c *conn) pingLoop() {
	ticker := time.NewTicker(c.cfg.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := c.writePing(); err != nil {
				_ = c.Close()
				return
			}
		case <-c.pingStop:
			return
		}
	}
}

func (c *conn) writePing() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.ws.SetWriteDeadline(time.Now().Add(c.cfg.WriteTimeout)); err != nil {
		return err
	}
	return c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(c.cfg.WriteTimeout))
}

func (c *conn) writeClose(code int, text string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.ws.SetWriteDeadline(time.Now().Add(c.cfg.WriteTimeout)); err != nil {
		return err
	}
	msg := websocket.FormatCloseMessage(code, text)
	return c.ws.WriteControl(websocket.CloseMessage, msg, time.Now().Add(c.cfg.WriteTimeout))
}

func negotiateSubprotocol(header string, supported []string) string {
	if len(supported) == 0 {
		return ""
	}
	offered := strings.Split(header, ",")
	for _, part := range offered {
		candidate := strings.TrimSpace(part)
		for _, want := range supported {
			if candidate == want {
				return want
			}
		}
	}
	return ""
}

func remoteHost(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return host
}

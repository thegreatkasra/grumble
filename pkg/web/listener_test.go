package web

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestConnectWithoutUpgradeReturns426(t *testing.T) {
	l := newTestListener(testListenerOptions{})
	req := httptest.NewRequest(http.MethodGet, "/connect", nil)
	rec := httptest.NewRecorder()
	l.ServeHTTP(rec, req)
	if rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("expected 426, got %d", rec.Code)
	}
}

func TestConnectReturns503WhenDisabled(t *testing.T) {
	l := newTestListener(testListenerOptions{disabled: true})
	req := httptest.NewRequest(http.MethodGet, "/connect", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()
	l.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestOriginAndSubprotocolValidation(t *testing.T) {
	l := newTestListener(testListenerOptions{})
	srv := httptest.NewServer(l)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/connect"
	dialer := websocket.Dialer{Subprotocols: []string{"mumble"}}

	cases := []struct {
		name          string
		origin        string
		subprotocols  []string
		wantHandshake bool
	}{
		{name: "allowed origin", origin: "https://allowed.example", subprotocols: []string{"mumble"}, wantHandshake: true},
		{name: "blocked origin", origin: "https://blocked.example", subprotocols: []string{"mumble"}, wantHandshake: false},
		{name: "missing origin", origin: "", subprotocols: []string{"mumble"}, wantHandshake: false},
		{name: "unsupported protocol", origin: "https://allowed.example", subprotocols: []string{"unknown"}, wantHandshake: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dialer.Subprotocols = tc.subprotocols
			header := http.Header{}
			if tc.origin != "" {
				header.Set("Origin", tc.origin)
			}
			conn, _, err := dialer.Dial(wsURL, header)
			if tc.wantHandshake {
				if err != nil {
					t.Fatalf("expected dial success: %v", err)
				}
				_ = conn.Close()
				return
			}
			if err == nil {
				_ = conn.Close()
				t.Fatal("expected dial failure")
			}
		})
	}
}

func TestTextFramesRejected(t *testing.T) {
	l := newTestListener(testListenerOptions{})
	srv := httptest.NewServer(l)
	defer srv.Close()

	clientConn := dialTestWebSocket(t, srv.URL)
	defer clientConn.Close()

	serverConn := acceptTestConn(t, l)
	defer serverConn.Close()

	if err := clientConn.WriteMessage(websocket.TextMessage, []byte("bad")); err != nil {
		t.Fatalf("text write failed: %v", err)
	}

	buf := make([]byte, 8)
	if _, err := serverConn.Read(buf); err == nil {
		t.Fatal("expected text frame read to fail")
	}
}

func TestBinaryFramesAccepted(t *testing.T) {
	l := newTestListener(testListenerOptions{})
	srv := httptest.NewServer(l)
	defer srv.Close()

	clientConn := dialTestWebSocket(t, srv.URL)
	defer clientConn.Close()

	serverConn := acceptTestConn(t, l)
	defer serverConn.Close()

	payload := []byte{1, 2, 3}
	if err := clientConn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatalf("binary write failed: %v", err)
	}

	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(serverConn, buf); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !bytes.Equal(buf, payload) {
		t.Fatalf("payload mismatch: got %v want %v", buf, payload)
	}
}

func TestFragmentedOversizedFrameRejected(t *testing.T) {
	l := newTestListener(testListenerOptions{maxMessageBytes: 32})
	srv := httptest.NewServer(l)
	defer srv.Close()

	clientConn := dialTestWebSocket(t, srv.URL)
	defer clientConn.Close()

	serverConn := acceptTestConn(t, l)
	defer serverConn.Close()

	writer, err := clientConn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		t.Fatalf("next writer failed: %v", err)
	}
	if _, err := writer.Write(bytes.Repeat([]byte{0xaa}, 64)); err != nil {
		t.Fatalf("write oversized frame failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	buf := make([]byte, 64)
	if _, err := serverConn.Read(buf); err == nil {
		t.Fatal("expected oversized websocket frame to be rejected")
	}
}

func TestPingPongKeepsConnectionAlive(t *testing.T) {
	l := newTestListener(testListenerOptions{
		idleTimeout:  300 * time.Millisecond,
		pingInterval: 50 * time.Millisecond,
	})
	srv := httptest.NewServer(l)
	defer srv.Close()

	clientConn := dialTestWebSocket(t, srv.URL)
	defer clientConn.Close()

	serverConn := acceptTestConn(t, l)
	defer serverConn.Close()

	var pingCount atomic.Int32
	messageCh := make(chan []byte, 1)
	readDone := make(chan struct{})
	clientConn.SetPingHandler(func(appData string) error {
		pingCount.Add(1)
		deadline := time.Now().Add(time.Second)
		return clientConn.WriteControl(websocket.PongMessage, []byte(appData), deadline)
	})
	go func() {
		defer close(readDone)
		for {
			mt, msg, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			if mt == websocket.BinaryMessage {
				messageCh <- msg
			}
		}
	}()

	time.Sleep(2 * l.cfg.IdleTimeout)
	if pingCount.Load() == 0 {
		t.Fatal("expected websocket ping/pong activity before idle timeout")
	}

	payload := []byte("still-alive")
	if _, err := serverConn.Write(payload); err != nil {
		t.Fatalf("server write after ping/pong failed: %v", err)
	}

	var buf []byte
	select {
	case buf = <-messageCh:
	case <-time.After(time.Second):
		t.Fatal("expected client to receive binary payload after ping/pong")
	}
	if !bytes.Equal(buf, payload) {
		t.Fatalf("payload mismatch: got %q want %q", buf, payload)
	}

	_ = clientConn.Close()
	<-readDone
}

func TestIdleTimeoutClosesInactiveConnection(t *testing.T) {
	l := newTestListener(testListenerOptions{
		idleTimeout:  150 * time.Millisecond,
		pingInterval: 50 * time.Millisecond,
	})
	srv := httptest.NewServer(l)
	defer srv.Close()

	clientConn := dialTestWebSocket(t, srv.URL)
	defer clientConn.Close()

	serverConn := acceptTestConn(t, l)
	defer serverConn.Close()

	buf := make([]byte, 1)
	if _, err := serverConn.Read(buf); err == nil {
		t.Fatal("expected idle timeout to close inactive connection")
	}
}

func TestWriteTimeout(t *testing.T) {
	ws := &fakeSocket{
		localAddr:  &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1},
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2},
		writeDelay: 5 * time.Millisecond,
	}
	serverConn := newConn(ws, ListenerConfig{
		MaxMessageBytes: 1024,
		IdleTimeout:     time.Second,
		WriteTimeout:    time.Millisecond,
		PingInterval:    time.Second,
	}, nil, "test-write-timeout")
	defer serverConn.Close()

	if _, err := serverConn.Write([]byte("late")); err == nil {
		t.Fatal("expected blocked write to fail with configured timeout")
	}
}

func TestConcurrentWrites(t *testing.T) {
	l := newTestListener(testListenerOptions{})
	srv := httptest.NewServer(l)
	defer srv.Close()

	clientConn := dialTestWebSocket(t, srv.URL)
	defer clientConn.Close()

	serverConn := acceptTestConn(t, l)
	defer serverConn.Close()

	payloads := [][]byte{
		[]byte("one"),
		[]byte("two"),
		[]byte("three"),
	}
	var wg sync.WaitGroup
	for _, payload := range payloads {
		payload := payload
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := serverConn.Write(payload); err != nil {
				t.Errorf("write failed: %v", err)
			}
		}()
	}
	wg.Wait()

	got := make(map[string]bool, len(payloads))
	for range payloads {
		if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatalf("set read deadline failed: %v", err)
		}
		mt, msg, err := clientConn.ReadMessage()
		if err != nil {
			t.Fatalf("read client message failed: %v", err)
		}
		if mt != websocket.BinaryMessage {
			t.Fatalf("expected binary message, got %d", mt)
		}
		got[string(msg)] = true
	}
	for _, payload := range payloads {
		if !got[string(payload)] {
			t.Fatalf("missing payload %q", payload)
		}
	}
}

func TestShutdownWithActiveClients(t *testing.T) {
	l := newTestListener(testListenerOptions{})
	srv := httptest.NewServer(l)
	defer srv.Close()

	clientConn := dialTestWebSocket(t, srv.URL)
	defer clientConn.Close()

	serverConn := acceptTestConn(t, l)
	defer serverConn.Close()

	if err := l.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline failed: %v", err)
	}
	if _, _, err := clientConn.ReadMessage(); err == nil {
		t.Fatal("expected active websocket client to be closed during listener shutdown")
	}
	if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("expected listener accept to report closed, got %v", err)
	}
	if _, err := serverConn.Write([]byte("after-close")); err == nil {
		t.Fatal("expected writes on closed server connection to fail")
	}
}

func TestAcceptCloseUnblocks(t *testing.T) {
	l := newTestListener(testListenerOptions{})
	done := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	if err := l.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected accept to unblock with error")
		}
	case <-time.After(time.Second):
		t.Fatal("accept did not unblock")
	}
}

func TestAcceptQueueSaturationAndRecovery(t *testing.T) {
	l := newTestListener(testListenerOptions{acceptQueueSize: 1})
	srv := httptest.NewServer(l)
	defer srv.Close()

	first := dialTestWebSocket(t, srv.URL)
	defer first.Close()

	second := dialTestWebSocket(t, srv.URL)
	defer second.Close()

	serverConn := acceptTestConn(t, l)
	defer serverConn.Close()

	third := dialTestWebSocket(t, srv.URL)
	defer third.Close()
}

func TestCloseIsSafeTwice(t *testing.T) {
	l := newTestListener(testListenerOptions{})
	if err := l.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}
	if err := l.Close(); err == nil {
		t.Fatal("expected second close to report closed listener")
	}
}

type testListenerOptions struct {
	disabled        bool
	maxMessageBytes int64
	acceptQueueSize int
	idleTimeout     time.Duration
	writeTimeout    time.Duration
	pingInterval    time.Duration
}

func newTestListener(opts testListenerOptions) *Listener {
	enabled := !opts.disabled
	maxMessageBytes := opts.maxMessageBytes
	if maxMessageBytes == 0 {
		maxMessageBytes = 1024
	}
	queueSize := opts.acceptQueueSize
	if queueSize == 0 {
		queueSize = 1
	}
	idleTimeout := opts.idleTimeout
	if idleTimeout == 0 {
		idleTimeout = time.Second
	}
	writeTimeout := opts.writeTimeout
	if writeTimeout == 0 {
		writeTimeout = time.Second
	}
	pingInterval := opts.pingInterval
	if pingInterval == 0 {
		pingInterval = 200 * time.Millisecond
	}

	return NewListener(ListenerConfig{
		Logger:          log.New(io.Discard, "", 0),
		Addr:            &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0},
		Path:            "/connect",
		Enabled:         enabled,
		MaxMessageBytes: maxMessageBytes,
		AcceptQueueSize: queueSize,
		IdleTimeout:     idleTimeout,
		WriteTimeout:    writeTimeout,
		PingInterval:    pingInterval,
		ValidateOrigin: func(origin string) bool {
			return origin == "https://allowed.example"
		},
		RequiredProtocols: []string{"mumble", "binary"},
	})
}

func dialTestWebSocket(t *testing.T, serverURL string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/connect"
	dialer := websocket.Dialer{Subprotocols: []string{"mumble"}}
	conn, _, err := dialer.Dial(wsURL, http.Header{"Origin": []string{"https://allowed.example"}})
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	return conn
}

func acceptTestConn(t *testing.T, l *Listener) net.Conn {
	t.Helper()
	conn, err := l.Accept()
	if err != nil {
		t.Fatalf("accept failed: %v", err)
	}
	return conn
}

type fakeSocket struct {
	localAddr  net.Addr
	remoteAddr net.Addr
	writeDelay time.Duration
	deadline   time.Time
}

func (f *fakeSocket) SetReadLimit(int64)                {}
func (f *fakeSocket) SetPongHandler(func(string) error) {}
func (f *fakeSocket) NextReader() (int, io.Reader, error) {
	return websocket.BinaryMessage, bytes.NewReader(nil), io.EOF
}
func (f *fakeSocket) Close() error                              { return nil }
func (f *fakeSocket) LocalAddr() net.Addr                       { return f.localAddr }
func (f *fakeSocket) RemoteAddr() net.Addr                      { return f.remoteAddr }
func (f *fakeSocket) SetReadDeadline(time.Time) error           { return nil }
func (f *fakeSocket) WriteControl(int, []byte, time.Time) error { return nil }
func (f *fakeSocket) SetWriteDeadline(t time.Time) error {
	f.deadline = t
	return nil
}
func (f *fakeSocket) WriteMessage(int, []byte) error {
	time.Sleep(f.writeDelay)
	if !f.deadline.IsZero() && time.Now().After(f.deadline) {
		return os.ErrDeadlineExceeded
	}
	return nil
}

package web

import (
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestConnectWithoutUpgradeReturns426(t *testing.T) {
	l := newTestListener(true)
	req := httptest.NewRequest(http.MethodGet, "/connect", nil)
	rec := httptest.NewRecorder()
	l.ServeHTTP(rec, req)
	if rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("expected 426, got %d", rec.Code)
	}
}

func TestConnectReturns503WhenDisabled(t *testing.T) {
	l := newTestListener(false)
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
	l := newTestListener(true)
	srv := httptest.NewServer(l)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/connect"
	dialer := websocket.Dialer{
		Subprotocols: []string{"mumble"},
	}

	header := http.Header{"Origin": []string{"https://allowed.example"}}
	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("expected allowed websocket dial to succeed: %v", err)
	}
	_ = conn.Close()

	header = http.Header{"Origin": []string{"https://blocked.example"}}
	if _, _, err := dialer.Dial(wsURL, header); err == nil {
		t.Fatal("expected blocked origin to fail")
	}

	header = http.Header{"Origin": []string{"https://allowed.example"}}
	dialer.Subprotocols = []string{"unknown"}
	if _, _, err := dialer.Dial(wsURL, header); err == nil {
		t.Fatal("expected unsupported subprotocol to fail")
	}
}

func TestTextFramesRejected(t *testing.T) {
	l := newTestListener(true)
	srv := httptest.NewServer(l)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/connect"
	dialer := websocket.Dialer{Subprotocols: []string{"mumble"}}
	conn, _, err := dialer.Dial(wsURL, http.Header{"Origin": []string{"https://allowed.example"}})
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	accepted, err := l.Accept()
	if err != nil {
		t.Fatalf("accept failed: %v", err)
	}
	defer accepted.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("bad")); err != nil {
		t.Fatalf("text write failed: %v", err)
	}

	buf := make([]byte, 8)
	if _, err := accepted.Read(buf); err == nil {
		t.Fatal("expected text frame read to fail")
	}
}

func TestAcceptCloseUnblocks(t *testing.T) {
	l := newTestListener(true)
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
	l := newTestListener(true)
	srv := httptest.NewServer(l)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/connect"
	dialer := websocket.Dialer{Subprotocols: []string{"mumble"}}
	header := http.Header{"Origin": []string{"https://allowed.example"}}

	first, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("first dial failed: %v", err)
	}
	defer first.Close()

	second, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("second dial failed: %v", err)
	}
	defer second.Close()

	serverConn, err := l.Accept()
	if err != nil {
		t.Fatalf("accept failed: %v", err)
	}
	defer serverConn.Close()

	if _, _, err := dialer.Dial(wsURL, header); err != nil {
		t.Fatalf("third dial after capacity freed failed: %v", err)
	}
}

func TestBinaryFramesAccepted(t *testing.T) {
	l := newTestListener(true)
	srv := httptest.NewServer(l)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/connect"
	dialer := websocket.Dialer{Subprotocols: []string{"mumble"}}
	clientConn, _, err := dialer.Dial(wsURL, http.Header{"Origin": []string{"https://allowed.example"}})
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer clientConn.Close()

	serverConn, err := l.Accept()
	if err != nil {
		t.Fatalf("accept failed: %v", err)
	}
	defer serverConn.Close()

	payload := []byte{1, 2, 3}
	if err := clientConn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatalf("binary write failed: %v", err)
	}

	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(serverConn, buf); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(buf) != string(payload) {
		t.Fatalf("payload mismatch: got %v want %v", buf, payload)
	}
}

func TestCloseIsSafeTwice(t *testing.T) {
	l := newTestListener(true)
	if err := l.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}
	if err := l.Close(); err == nil {
		t.Fatal("expected second close to report closed listener")
	}
}

func newTestListener(enabled bool) *Listener {
	return NewListener(ListenerConfig{
		Logger:          log.New(io.Discard, "", 0),
		Addr:            &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0},
		Path:            "/connect",
		Enabled:         enabled,
		MaxMessageBytes: 1024,
		AcceptQueueSize: 1,
		IdleTimeout:     time.Second,
		WriteTimeout:    time.Second,
		PingInterval:    200 * time.Millisecond,
		ValidateOrigin: func(origin string) bool {
			return origin == "https://allowed.example"
		},
		RequiredProtocols: []string{"mumble", "binary"},
	})
}

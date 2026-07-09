package main

import (
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mumble.info/grumble/pkg/blobstore"
)

func TestServerStartListenerMapWithoutUDP(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	if server.webtcpl == nil {
		t.Fatal("expected web tcp listener to be bound")
	}
	if server.tcpl == nil {
		t.Fatal("expected raw mumble tcp listener to be bound")
	}
	if server.udpconn != nil {
		t.Fatal("expected udp listener to remain nil")
	}
	if server.webhttp == nil || server.webwsl == nil {
		t.Fatal("expected http/websocket stack to be initialized")
	}

	if runtimeState.checks["udp"] != "disabled" {
		t.Fatalf("expected readiness udp check to be disabled, got %q", runtimeState.checks["udp"])
	}
}

func TestServerStartWithoutRawTCP(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, false)
	defer cleanup()

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	if server.webtcpl == nil {
		t.Fatal("expected web tcp listener to be bound")
	}
	if server.tcpl != nil {
		t.Fatal("expected raw mumble tcp listener to stay disabled")
	}
	if server.udpconn != nil {
		t.Fatal("expected udp listener to remain nil")
	}
}

func TestServerStopIsSafeWithNilListenersAndRepeatedCalls(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, false)
	defer cleanup()

	if err := server.Stop(); err != nil {
		t.Fatalf("expected stop before start to be safe, got %v", err)
	}

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if err := server.Stop(); err != nil {
		t.Fatalf("first stop failed: %v", err)
	}
	if err := server.Stop(); err != nil {
		t.Fatalf("second stop failed: %v", err)
	}
}

func TestReserveConnectionEnforcesGlobalAndPerIPLimits(t *testing.T) {
	runtimeConfig = RuntimeConfig{
		TeamlancerMode:      true,
		MaxConnections:      2,
		MaxConnectionsPerIP: 1,
	}
	server, err := NewServer(1)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	server.initPerLaunchData()
	defer server.cleanPerLaunchData()

	addr1 := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1111}
	addr2 := &net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 2222}

	if !server.reserveConnection(addr1) {
		t.Fatal("expected first connection to reserve")
	}
	if server.reserveConnection(addr1) {
		t.Fatal("expected per-ip limit to reject second connection from same ip")
	}
	if !server.reserveConnection(addr2) {
		t.Fatal("expected different ip to reserve")
	}
	if server.reserveConnection(&net.TCPAddr{IP: net.ParseIP("127.0.0.3"), Port: 3333}) {
		t.Fatal("expected global limit to reject third concurrent connection")
	}
}

func TestReleaseReservedConnectionIsIdempotent(t *testing.T) {
	runtimeConfig = RuntimeConfig{
		TeamlancerMode:      true,
		MaxConnections:      2,
		MaxConnectionsPerIP: 2,
	}
	server, err := NewServer(1)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	server.initPerLaunchData()
	defer server.cleanPerLaunchData()

	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1111}
	if !server.reserveConnection(addr) {
		t.Fatal("expected reserve to succeed")
	}
	server.releaseReservedConnection(addr)
	server.releaseReservedConnection(addr)

	if got := server.totalConns.Load(); got != 0 {
		t.Fatalf("expected total connections to be zero, got %d", got)
	}
	if got := server.ipConns["127.0.0.1"]; got != 0 {
		t.Fatalf("expected per-ip count to be zero, got %d", got)
	}
}

func newTeamlancerTestServer(t *testing.T, enableRawTCP bool) (*Server, func()) {
	t.Helper()

	runtimeState = newAppRuntimeState()
	tempDir := t.TempDir()
	Args.DataDir = tempDir
	blobDir := filepath.Join(tempDir, "blob")
	if err := os.MkdirAll(blobDir, 0o700); err != nil {
		t.Fatalf("mkdir blob: %v", err)
	}
	serverDir := filepath.Join(tempDir, "servers", "1")
	if err := os.MkdirAll(serverDir, 0o700); err != nil {
		t.Fatalf("mkdir server: %v", err)
	}
	blobStore = blobstore.Open(blobDir)
	if enableRawTCP {
		if err := GenerateSelfSignedCert(filepath.Join(tempDir, "cert.pem"), filepath.Join(tempDir, "key.pem")); err != nil {
			t.Fatalf("generate cert: %v", err)
		}
	}

	webPort := reserveTCPPort(t)
	rawPort := reserveTCPPort(t)
	runtimeConfig = RuntimeConfig{
		TeamlancerMode:          true,
		WebBindAddress:          "127.0.0.1",
		WebPort:                 webPort,
		EnableWeb:               true,
		WebSocketPath:           "/connect",
		RawMumbleTCPBindAddress: "127.0.0.1",
		RawMumbleTCPPort:        rawPort,
		EnableRawMumbleTCP:      enableRawTCP,
		EnableUDP:               false,
		HealthPath:              "/health",
		ReadinessPath:           "/ready",
		DataDir:                 tempDir,
		AllowedOrigins:          []Origin{{Scheme: "https", Host: "teamlancer.work"}},
		WSMaxMessageBytes:       1024,
		WSAcceptQueueSize:       4,
		WSIdleTimeout:           5 * time.Second,
		WSWriteTimeout:          2 * time.Second,
		WSPingInterval:          time.Second,
		MaxConnections:          16,
		MaxConnectionsPerIP:     4,
		ShutdownTimeout:         2 * time.Second,
	}

	server, err := NewServer(1)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	server.Logger = log.New(io.Discard, "", 0)

	cleanup := func() {
		_ = server.Stop()
	}
	return server, cleanup
}

func reserveTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

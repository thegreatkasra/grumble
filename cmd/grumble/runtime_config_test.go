package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeConfigValidDefaults(t *testing.T) {
	cfg := RuntimeConfig{
		TeamlancerMode:          true,
		WebBindAddress:          "0.0.0.0",
		WebPort:                 7880,
		EnableWeb:               true,
		WebSocketPath:           "/connect",
		RawMumbleTCPBindAddress: "0.0.0.0",
		RawMumbleTCPPort:        64738,
		EnableRawMumbleTCP:      true,
		EnableUDP:               false,
		HealthPath:              "/health",
		ReadinessPath:           "/ready",
		DataDir:                 "/data",
		LogLevel:                "info",
		LogFormat:               "json",
		AllowedOrigins: []Origin{
			{Scheme: "https", Host: "teamlancer.work"},
			{Scheme: "https", Host: "app.teamlancer.work"},
		},
		WSMaxMessageBytes:     1048576,
		WSAcceptQueueSize:     128,
		WSIdleTimeout:         90 * time.Second,
		WSWriteTimeout:        15 * time.Second,
		WSPingInterval:        30 * time.Second,
		MaxConnections:        1000,
		MaxConnectionsPerIP:   20,
		ShutdownTimeout:       20 * time.Second,
		EnablePublicWebSocket: false,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected config to be valid, got %v", err)
	}
}

func TestRuntimeConfigRejectsInvalidPorts(t *testing.T) {
	cfg := RuntimeConfig{
		WebBindAddress:          "0.0.0.0",
		WebPort:                 0,
		WebSocketPath:           "/connect",
		RawMumbleTCPBindAddress: "0.0.0.0",
		RawMumbleTCPPort:        64738,
		HealthPath:              "/health",
		ReadinessPath:           "/ready",
		WSMaxMessageBytes:       1,
		WSAcceptQueueSize:       1,
		WSIdleTimeout:           2 * time.Second,
		WSWriteTimeout:          1 * time.Second,
		WSPingInterval:          1 * time.Second,
		MaxConnections:          1,
		MaxConnectionsPerIP:     1,
		ShutdownTimeout:         1 * time.Second,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid web port to fail")
	}
	cfg.WebPort = 7880
	cfg.RawMumbleTCPPort = 70000
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid raw tcp port to fail")
	}
}

func TestRuntimeConfigRejectsIdenticalPorts(t *testing.T) {
	cfg := validRuntimeConfigForTests()
	cfg.RawMumbleTCPPort = cfg.WebPort
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected identical ports to fail")
	}
}

func TestRuntimeConfigRejectsInvalidPaths(t *testing.T) {
	cfg := validRuntimeConfigForTests()
	cfg.WebSocketPath = "connect"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid websocket path to fail")
	}
}

func TestRuntimeConfigRejectsInvalidOriginEntries(t *testing.T) {
	if _, err := parseAllowedOrigins("not-an-origin", false); err == nil {
		t.Fatal("expected origin parsing to fail")
	}
}

func TestAllowedOriginMatrix(t *testing.T) {
	runtimeConfig = RuntimeConfig{
		AllowDevelopmentOrigins: false,
		AllowedOrigins: []Origin{
			{Scheme: "https", Host: "teamlancer.work"},
		},
	}

	cases := []struct {
		name   string
		origin string
		want   bool
	}{
		{name: "allowed production origin", origin: "https://teamlancer.work", want: true},
		{name: "blocked origin", origin: "https://blocked.teamlancer.work", want: false},
		{name: "wrong scheme", origin: "http://teamlancer.work", want: false},
		{name: "wrong port", origin: "https://teamlancer.work:8443", want: false},
		{name: "missing origin", origin: "", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAllowedOrigin(tc.origin); got != tc.want {
				t.Fatalf("expected %v for %q, got %v", tc.want, tc.origin, got)
			}
		})
	}
}

func TestAllowedOriginAllowsLocalhostInDevelopmentMode(t *testing.T) {
	runtimeConfig = RuntimeConfig{
		AllowDevelopmentOrigins: true,
		AllowedOrigins: []Origin{
			{Scheme: "http", Host: "localhost:3000"},
		},
	}
	if !isAllowedOrigin("http://localhost:3000") {
		t.Fatal("expected localhost development origin to be allowed")
	}
}

func TestRuntimeConfigRejectsInvalidLimitsAndTimeouts(t *testing.T) {
	cfg := validRuntimeConfigForTests()
	cfg.WSMaxMessageBytes = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid message limit to fail")
	}
	cfg = validRuntimeConfigForTests()
	cfg.WSPingInterval = cfg.WSIdleTimeout
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid timeout relationship to fail")
	}
}

func TestRuntimeConfigRejectsUDPInTeamlancerMode(t *testing.T) {
	cfg := validRuntimeConfigForTests()
	cfg.TeamlancerMode = true
	cfg.EnableUDP = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected UDP in Teamlancer mode to fail")
	}
}

func TestLoadRuntimeConfigRejectsMalformedValues(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{name: "bool", key: "TEAMLANCER_MODE", value: "maybe", want: "TEAMLANCER_MODE"},
		{name: "port", key: "WEB_PORT", value: "abc", want: "WEB_PORT"},
		{name: "udp bool", key: "ENABLE_UDP", value: "yes-please", want: "ENABLE_UDP"},
		{name: "message size", key: "WS_MAX_MESSAGE_BYTES", value: "-5", want: "WS_MAX_MESSAGE_BYTES"},
		{name: "duration", key: "WS_IDLE_TIMEOUT_SECONDS", value: "zero", want: "WS_IDLE_TIMEOUT_SECONDS"},
		{name: "connection limit", key: "MAX_CONNECTIONS", value: "0", want: "MAX_CONNECTIONS"},
		{name: "origins", key: "ALLOWED_ORIGINS", value: "invalid", want: "invalid origin"},
		{name: "ip", key: "WEB_BIND_ADDRESS", value: "not-an-ip", want: "WEB_BIND_ADDRESS"},
		{name: "path", key: "DATA_DIR", value: "relative/path", want: "DATA_DIR"},
		{name: "queue", key: "WS_ACCEPT_QUEUE_SIZE", value: "0", want: "WS_ACCEPT_QUEUE_SIZE"},
		{name: "shutdown", key: "SHUTDOWN_TIMEOUT_SECONDS", value: "0", want: "SHUTDOWN_TIMEOUT_SECONDS"},
		{name: "dev origins bool", key: "ALLOW_DEVELOPMENT_ORIGINS", value: "invalid", want: "ALLOW_DEVELOPMENT_ORIGINS"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setValidRuntimeEnv(t)
			t.Setenv(tc.key, tc.value)

			_, err := LoadRuntimeConfig()
			if err == nil {
				t.Fatalf("expected %s to fail", tc.key)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error %q to contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLoadRuntimeConfigUsesDefaultsWhenEnvAbsent(t *testing.T) {
	Args.DataDir = filepath.Clean(defaultDataDir())
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		t.Fatalf("expected defaults to load, got %v", err)
	}
	if cfg.WebPort != 7880 {
		t.Fatalf("expected default web port, got %d", cfg.WebPort)
	}
	if cfg.EnableUDP {
		t.Fatal("expected UDP to default disabled")
	}
}

func validRuntimeConfigForTests() RuntimeConfig {
	return RuntimeConfig{
		WebBindAddress:          "0.0.0.0",
		WebPort:                 7880,
		WebSocketPath:           "/connect",
		RawMumbleTCPBindAddress: "0.0.0.0",
		RawMumbleTCPPort:        64738,
		HealthPath:              "/health",
		ReadinessPath:           "/ready",
		AllowedOrigins: []Origin{
			{Scheme: "https", Host: "teamlancer.work"},
		},
		WSMaxMessageBytes:   1,
		WSAcceptQueueSize:   1,
		WSIdleTimeout:       2 * time.Second,
		WSWriteTimeout:      1 * time.Second,
		WSPingInterval:      1 * time.Second,
		MaxConnections:      1,
		MaxConnectionsPerIP: 1,
		ShutdownTimeout:     1 * time.Second,
	}
}

func setValidRuntimeEnv(t *testing.T) {
	t.Helper()
	Args.DataDir = filepath.Clean(defaultDataDir())
	t.Setenv("TEAMLANCER_MODE", "true")
	t.Setenv("WEB_BIND_ADDRESS", "0.0.0.0")
	t.Setenv("WEB_PORT", "7880")
	t.Setenv("ENABLE_WEB", "true")
	t.Setenv("WEBSOCKET_PATH", "/connect")
	t.Setenv("RAW_MUMBLE_TCP_BIND_ADDRESS", "0.0.0.0")
	t.Setenv("RAW_MUMBLE_TCP_PORT", "64738")
	t.Setenv("ENABLE_RAW_MUMBLE_TCP", "true")
	t.Setenv("ENABLE_UDP", "false")
	t.Setenv("HEALTH_PATH", "/health")
	t.Setenv("READINESS_PATH", "/ready")
	t.Setenv("DATA_DIR", filepath.Clean(defaultDataDir()))
	t.Setenv("ALLOWED_ORIGINS", "https://teamlancer.work,https://app.teamlancer.work")
	t.Setenv("ALLOW_DEVELOPMENT_ORIGINS", "false")
	t.Setenv("WS_MAX_MESSAGE_BYTES", "1048576")
	t.Setenv("WS_ACCEPT_QUEUE_SIZE", "128")
	t.Setenv("WS_IDLE_TIMEOUT_SECONDS", "90")
	t.Setenv("WS_WRITE_TIMEOUT_SECONDS", "15")
	t.Setenv("WS_PING_INTERVAL_SECONDS", "30")
	t.Setenv("MAX_CONNECTIONS", "1000")
	t.Setenv("MAX_CONNECTIONS_PER_IP", "20")
	t.Setenv("SHUTDOWN_TIMEOUT_SECONDS", "20")
	t.Setenv("ENABLE_PUBLIC_WEBSOCKET", "false")
}

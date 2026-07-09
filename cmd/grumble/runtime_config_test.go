package main

import (
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

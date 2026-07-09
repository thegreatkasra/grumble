package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type RuntimeConfig struct {
	TeamlancerMode            bool
	WebBindAddress            string
	WebPort                   int
	EnableWeb                 bool
	WebSocketPath             string
	RawMumbleTCPBindAddress   string
	RawMumbleTCPPort          int
	EnableRawMumbleTCP        bool
	EnableUDP                 bool
	HealthPath                string
	ReadinessPath             string
	DataDir                   string
	LogLevel                  string
	LogFormat                 string
	AllowedOrigins            []Origin
	AllowDevelopmentOrigins   bool
	WSMaxMessageBytes         int64
	WSAcceptQueueSize         int
	WSIdleTimeout             time.Duration
	WSWriteTimeout            time.Duration
	WSPingInterval            time.Duration
	MaxConnections            int
	MaxConnectionsPerIP       int
	ShutdownTimeout           time.Duration
	EnablePublicWebSocket     bool
	TrustProxyHeaders         bool
}

type Origin struct {
	Scheme string
	Host   string
}

func LoadRuntimeConfig() (RuntimeConfig, error) {
	cfg := RuntimeConfig{
		TeamlancerMode:          getEnvBool("TEAMLANCER_MODE", false),
		WebBindAddress:          getEnv("WEB_BIND_ADDRESS", "0.0.0.0"),
		WebPort:                 getEnvInt("WEB_PORT", 7880),
		EnableWeb:               getEnvBool("ENABLE_WEB", true),
		WebSocketPath:           getEnv("WEBSOCKET_PATH", "/connect"),
		RawMumbleTCPBindAddress: getEnv("RAW_MUMBLE_TCP_BIND_ADDRESS", "0.0.0.0"),
		RawMumbleTCPPort:        getEnvInt("RAW_MUMBLE_TCP_PORT", 64738),
		EnableRawMumbleTCP:      getEnvBool("ENABLE_RAW_MUMBLE_TCP", true),
		EnableUDP:               getEnvBool("ENABLE_UDP", false),
		HealthPath:              getEnv("HEALTH_PATH", "/health"),
		ReadinessPath:           getEnv("READINESS_PATH", "/ready"),
		DataDir:                 getEnv("DATA_DIR", Args.DataDir),
		LogLevel:                getEnv("LOG_LEVEL", "info"),
		LogFormat:               getEnv("LOG_FORMAT", "json"),
		AllowDevelopmentOrigins: getEnvBool("ALLOW_DEVELOPMENT_ORIGINS", false),
		WSMaxMessageBytes:       int64(getEnvInt("WS_MAX_MESSAGE_BYTES", 1048576)),
		WSAcceptQueueSize:       getEnvInt("WS_ACCEPT_QUEUE_SIZE", 128),
		WSIdleTimeout:           time.Duration(getEnvInt("WS_IDLE_TIMEOUT_SECONDS", 90)) * time.Second,
		WSWriteTimeout:          time.Duration(getEnvInt("WS_WRITE_TIMEOUT_SECONDS", 15)) * time.Second,
		WSPingInterval:          time.Duration(getEnvInt("WS_PING_INTERVAL_SECONDS", 30)) * time.Second,
		MaxConnections:          getEnvInt("MAX_CONNECTIONS", 1000),
		MaxConnectionsPerIP:     getEnvInt("MAX_CONNECTIONS_PER_IP", 20),
		ShutdownTimeout:         time.Duration(getEnvInt("SHUTDOWN_TIMEOUT_SECONDS", 20)) * time.Second,
		EnablePublicWebSocket:   getEnvBool("ENABLE_PUBLIC_WEBSOCKET", false),
		TrustProxyHeaders:       getEnvBool("TRUST_PROXY_HEADERS", false),
	}

	origins, err := parseAllowedOrigins(getEnv("ALLOWED_ORIGINS", "https://teamlancer.work,https://app.teamlancer.work"), cfg.AllowDevelopmentOrigins)
	if err != nil {
		return RuntimeConfig{}, err
	}
	cfg.AllowedOrigins = origins

	if err := cfg.Validate(); err != nil {
		return RuntimeConfig{}, err
	}
	return cfg, nil
}

func (cfg RuntimeConfig) Validate() error {
	if err := validateTCPPort(cfg.WebPort, "WEB_PORT"); err != nil {
		return err
	}
	if err := validateTCPPort(cfg.RawMumbleTCPPort, "RAW_MUMBLE_TCP_PORT"); err != nil {
		return err
	}
	for name, path := range map[string]string{
		"WEBSOCKET_PATH": cfg.WebSocketPath,
		"HEALTH_PATH":    cfg.HealthPath,
		"READINESS_PATH": cfg.ReadinessPath,
	} {
		if !strings.HasPrefix(path, "/") {
			return fmt.Errorf("%s must start with /", name)
		}
	}
	if cfg.WebPort == cfg.RawMumbleTCPPort {
		return errors.New("WEB_PORT and RAW_MUMBLE_TCP_PORT must differ")
	}
	if cfg.WSMaxMessageBytes <= 0 {
		return errors.New("WS_MAX_MESSAGE_BYTES must be greater than zero")
	}
	if cfg.WSAcceptQueueSize <= 0 {
		return errors.New("WS_ACCEPT_QUEUE_SIZE must be greater than zero")
	}
	if cfg.WSIdleTimeout <= 0 || cfg.WSWriteTimeout <= 0 || cfg.WSPingInterval <= 0 {
		return errors.New("WebSocket timeouts must be greater than zero")
	}
	if cfg.WSPingInterval >= cfg.WSIdleTimeout {
		return errors.New("WS_PING_INTERVAL_SECONDS must be lower than WS_IDLE_TIMEOUT_SECONDS")
	}
	if cfg.MaxConnections <= 0 || cfg.MaxConnectionsPerIP <= 0 {
		return errors.New("connection limits must be greater than zero")
	}
	if cfg.ShutdownTimeout <= 0 {
		return errors.New("SHUTDOWN_TIMEOUT_SECONDS must be greater than zero")
	}
	if cfg.TeamlancerMode && cfg.EnableUDP {
		return errors.New("ENABLE_UDP=true is invalid when TEAMLANCER_MODE=true")
	}
	if ip := net.ParseIP(cfg.WebBindAddress); ip == nil {
		return fmt.Errorf("invalid WEB_BIND_ADDRESS: %q", cfg.WebBindAddress)
	}
	if ip := net.ParseIP(cfg.RawMumbleTCPBindAddress); ip == nil {
		return fmt.Errorf("invalid RAW_MUMBLE_TCP_BIND_ADDRESS: %q", cfg.RawMumbleTCPBindAddress)
	}
	return nil
}

func (cfg RuntimeConfig) VerifyDataDirWritable() error {
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return err
	}
	testFile := filepath.Join(cfg.DataDir, ".writecheck")
	if err := os.WriteFile(testFile, []byte("ok"), 0600); err != nil {
		return err
	}
	return os.Remove(testFile)
}

func parseAllowedOrigins(raw string, allowDevelopment bool) ([]Origin, error) {
	parts := strings.Split(raw, ",")
	origins := make([]Origin, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		u, err := url.Parse(part)
		if err != nil || u.Scheme == "" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return nil, fmt.Errorf("invalid origin %q", part)
		}
		origin := Origin{
			Scheme: strings.ToLower(u.Scheme),
			Host:   strings.ToLower(u.Host),
		}
		if isDevelopmentOrigin(origin) && !allowDevelopment {
			return nil, fmt.Errorf("development origin %q requires ALLOW_DEVELOPMENT_ORIGINS=true", part)
		}
		origins = append(origins, origin)
	}
	return origins, nil
}

func isDevelopmentOrigin(origin Origin) bool {
	host := origin.Host
	if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") || strings.HasPrefix(host, "[::1]") {
		return true
	}
	return false
}

func validateTCPPort(port int, name string) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%s must be between 1 and 65535", name)
	}
	return nil
}

func runtimeTCPAddr(host string, port int) *net.TCPAddr {
	return &net.TCPAddr{IP: net.ParseIP(host), Port: port}
}

func isAllowedOrigin(originHeader string) bool {
	if originHeader == "" {
		return false
	}
	origins, err := parseAllowedOrigins(originHeader, runtimeConfig.AllowDevelopmentOrigins)
	if err != nil || len(origins) != 1 {
		return false
	}
	origin := origins[0]
	for _, allowed := range runtimeConfig.AllowedOrigins {
		if allowed.Scheme == origin.Scheme && allowed.Host == origin.Host {
			return true
		}
	}
	return false
}

func getEnv(key, fallback string) string {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	return val
}

func getEnvBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

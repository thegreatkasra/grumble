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

type envValue struct {
	name    string
	value   string
	present bool
}

func LoadRuntimeConfig() (RuntimeConfig, error) {
	var err error
	cfg := RuntimeConfig{
		WebSocketPath: "/connect",
		HealthPath:    "/health",
		ReadinessPath: "/ready",
		LogLevel:      "info",
		LogFormat:     "json",
	}

	if cfg.TeamlancerMode, err = getEnvBoolStrict("TEAMLANCER_MODE", false); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.WebBindAddress, err = getEnvIP("WEB_BIND_ADDRESS", "0.0.0.0"); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.WebPort, err = getEnvPositiveInt("WEB_PORT", 7880); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.EnableWeb, err = getEnvBoolStrict("ENABLE_WEB", true); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.WebSocketPath, err = getEnvPath("WEBSOCKET_PATH", cfg.WebSocketPath); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.RawMumbleTCPBindAddress, err = getEnvIP("RAW_MUMBLE_TCP_BIND_ADDRESS", "0.0.0.0"); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.RawMumbleTCPPort, err = getEnvPositiveInt("RAW_MUMBLE_TCP_PORT", 64738); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.EnableRawMumbleTCP, err = getEnvBoolStrict("ENABLE_RAW_MUMBLE_TCP", true); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.EnableUDP, err = getEnvBoolStrict("ENABLE_UDP", false); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.HealthPath, err = getEnvPath("HEALTH_PATH", cfg.HealthPath); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.ReadinessPath, err = getEnvPath("READINESS_PATH", cfg.ReadinessPath); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.DataDir, err = getEnvAbsPath("DATA_DIR", Args.DataDir); err != nil {
		return RuntimeConfig{}, err
	}
	cfg.LogLevel = getEnv("LOG_LEVEL", cfg.LogLevel)
	cfg.LogFormat = getEnv("LOG_FORMAT", cfg.LogFormat)
	if cfg.AllowDevelopmentOrigins, err = getEnvBoolStrict("ALLOW_DEVELOPMENT_ORIGINS", false); err != nil {
		return RuntimeConfig{}, err
	}
	wsMaxMessageBytes, err := getEnvPositiveInt("WS_MAX_MESSAGE_BYTES", 1048576)
	if err != nil {
		return RuntimeConfig{}, err
	}
	cfg.WSMaxMessageBytes = int64(wsMaxMessageBytes)
	if cfg.WSAcceptQueueSize, err = getEnvPositiveInt("WS_ACCEPT_QUEUE_SIZE", 128); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.WSIdleTimeout, err = getEnvPositiveSeconds("WS_IDLE_TIMEOUT_SECONDS", 90); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.WSWriteTimeout, err = getEnvPositiveSeconds("WS_WRITE_TIMEOUT_SECONDS", 15); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.WSPingInterval, err = getEnvPositiveSeconds("WS_PING_INTERVAL_SECONDS", 30); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.MaxConnections, err = getEnvPositiveInt("MAX_CONNECTIONS", 1000); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.MaxConnectionsPerIP, err = getEnvPositiveInt("MAX_CONNECTIONS_PER_IP", 20); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.ShutdownTimeout, err = getEnvPositiveSeconds("SHUTDOWN_TIMEOUT_SECONDS", 20); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.EnablePublicWebSocket, err = getEnvBoolStrict("ENABLE_PUBLIC_WEBSOCKET", false); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.TrustProxyHeaders, err = getEnvBoolStrict("TRUST_PROXY_HEADERS", false); err != nil {
		return RuntimeConfig{}, err
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
		if origin.Scheme != "http" && origin.Scheme != "https" {
			return nil, fmt.Errorf("invalid origin %q: scheme must be http or https", part)
		}
		if isDevelopmentOrigin(origin) && !allowDevelopment {
			return nil, fmt.Errorf("development origin %q requires ALLOW_DEVELOPMENT_ORIGINS=true", part)
		}
		origins = append(origins, origin)
	}
	if len(origins) == 0 {
		return nil, errors.New("ALLOWED_ORIGINS must contain at least one origin")
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

func lookupEnv(key string) envValue {
	raw, present := os.LookupEnv(key)
	return envValue{
		name:    key,
		value:   strings.TrimSpace(raw),
		present: present,
	}
}

func getEnvBoolStrict(key string, fallback bool) (bool, error) {
	env := lookupEnv(key)
	if !env.present || env.value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(env.value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return parsed, nil
}

func getEnvPositiveInt(key string, fallback int) (int, error) {
	env := lookupEnv(key)
	if !env.present || env.value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(env.value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", key)
	}
	return parsed, nil
}

func getEnvPositiveSeconds(key string, fallbackSeconds int) (time.Duration, error) {
	seconds, err := getEnvPositiveInt(key, fallbackSeconds)
	if err != nil {
		return 0, err
	}
	return time.Duration(seconds) * time.Second, nil
}

func getEnvPath(key, fallback string) (string, error) {
	env := lookupEnv(key)
	if !env.present || env.value == "" {
		return fallback, nil
	}
	if !strings.HasPrefix(env.value, "/") {
		return "", fmt.Errorf("%s must start with /", key)
	}
	if strings.ContainsAny(env.value, "?#") {
		return "", fmt.Errorf("%s must not contain query or fragment characters", key)
	}
	return env.value, nil
}

func getEnvAbsPath(key, fallback string) (string, error) {
	env := lookupEnv(key)
	if !env.present || env.value == "" {
		return fallback, nil
	}
	if !filepath.IsAbs(env.value) {
		return "", fmt.Errorf("%s must be an absolute path", key)
	}
	return env.value, nil
}

func getEnvIP(key, fallback string) (string, error) {
	env := lookupEnv(key)
	if !env.present || env.value == "" {
		return fallback, nil
	}
	if net.ParseIP(env.value) == nil {
		return "", fmt.Errorf("%s must be a valid IP address", key)
	}
	return env.value, nil
}

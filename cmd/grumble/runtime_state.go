package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"mumble.info/grumble/pkg/web"
)

var runtimeConfig RuntimeConfig
var runtimeState = newAppRuntimeState()

type appRuntimeState struct {
	started      atomic.Int32
	shuttingDown atomic.Int32

	mu     sync.RWMutex
	checks map[string]string
}

func newAppRuntimeState() *appRuntimeState {
	s := &appRuntimeState{}
	s.checks = map[string]string{
		"dataDirectory":       "starting",
		"webListener":         "starting",
		"rawMumbleTcpListener": "starting",
		"udp":                 "disabled",
		"virtualServer":       "starting",
	}
	return s
}

func (s *appRuntimeState) SetCheck(name, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks[name] = status
}

func (s *appRuntimeState) MarkReady() {
	s.started.Store(1)
}

func (s *appRuntimeState) MarkShuttingDown() {
	s.shuttingDown.Store(1)
}

func (s *appRuntimeState) IsReady() bool {
	if s.started.Load() != 1 || s.shuttingDown.Load() != 0 {
		return false
	}
	required := s.requiredChecks()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for name, expected := range required {
		if s.checks[name] != expected {
			return false
		}
	}
	return true
}

func (s *appRuntimeState) requiredChecks() map[string]string {
	required := map[string]string{
		"dataDirectory": "ok",
		"virtualServer": "ok",
	}
	if runtimeConfig.TeamlancerMode {
		required["udp"] = "disabled"
		if runtimeConfig.EnableWeb {
			required["webListener"] = "ok"
		}
		if runtimeConfig.EnableRawMumbleTCP {
			required["rawMumbleTcpListener"] = "ok"
		}
		return required
	}
	required["webListener"] = "ok"
	required["rawMumbleTcpListener"] = "ok"
	return required
}

func (s *appRuntimeState) HealthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *appRuntimeState) ReadyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if !s.IsReady() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "starting"})
		return
	}

	s.mu.RLock()
	checks := make(map[string]string, len(s.checks))
	for k, v := range s.checks {
		checks[k] = v
	}
	s.mu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "ready",
		"checks": checks,
	})
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func newWebListener(logger *log.Logger) *web.Listener {
	enabled := true
	validateOrigin := func(string) bool { return true }
	maxMessageBytes := int64(8 * 1024 * 1024)
	queueSize := 128
	idleTimeout := 2 * time.Minute
	writeTimeout := 15 * time.Second
	pingInterval := 30 * time.Second
	path := "/"
	addr := runtimeTCPAddr(runtimeConfig.WebBindAddress, runtimeConfig.WebPort)

	if runtimeConfig.TeamlancerMode {
		enabled = runtimeConfig.EnablePublicWebSocket
		validateOrigin = isAllowedOrigin
		maxMessageBytes = runtimeConfig.WSMaxMessageBytes
		queueSize = runtimeConfig.WSAcceptQueueSize
		idleTimeout = runtimeConfig.WSIdleTimeout
		writeTimeout = runtimeConfig.WSWriteTimeout
		pingInterval = runtimeConfig.WSPingInterval
		path = runtimeConfig.WebSocketPath
		addr = runtimeTCPAddr(runtimeConfig.WebBindAddress, runtimeConfig.WebPort)
	}
	return web.NewListener(web.ListenerConfig{
		Logger:            logger,
		Addr:              addr,
		Path:              path,
		Enabled:           enabled,
		MaxMessageBytes:   maxMessageBytes,
		AcceptQueueSize:   queueSize,
		IdleTimeout:       idleTimeout,
		WriteTimeout:      writeTimeout,
		PingInterval:      pingInterval,
		ValidateOrigin:    validateOrigin,
		RequiredProtocols: []string{"mumble", "binary"},
	})
}

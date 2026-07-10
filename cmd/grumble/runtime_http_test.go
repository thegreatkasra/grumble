package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthEndpoint(t *testing.T) {
	state := newAppRuntimeState()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	state.HealthHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected application/json, got %q", got)
	}
	var payload struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unable to decode body: %v", err)
	}
	if payload.Status != "ok" {
		t.Fatalf("expected status ok, got %q", payload.Status)
	}
}

func TestHealthEndpointRejectsNonGet(t *testing.T) {
	state := newAppRuntimeState()
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()
	state.HealthHandler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestReadyEndpointBeforeAndAfterInitialization(t *testing.T) {
	runtimeConfig = RuntimeConfig{
		TeamlancerMode:     true,
		EnableWeb:          true,
		EnableRawMumbleTCP: true,
	}
	state := newAppRuntimeState()
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)

	rec := httptest.NewRecorder()
	state.ReadyHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 before ready, got %d", rec.Code)
	}
	var notReady struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &notReady); err != nil {
		t.Fatalf("unable to decode not ready body: %v", err)
	}
	if notReady.Status != "not_ready" {
		t.Fatalf("expected not_ready status before ready, got %q", notReady.Status)
	}
	if len(notReady.Checks) == 0 {
		t.Fatal("expected readiness checks to be preserved before ready")
	}

	state.SetCheck("dataDirectory", "ok")
	state.SetCheck("voiceEngine", "ok")
	state.SetCheck("auth", "ok")
	state.SetCheck("webListener", "ok")
	state.SetCheck("websocket", "ok")
	state.SetCheck("rawMumbleTcpListener", "ok")
	state.SetCheck("udp", "disabled")
	state.SetCheck("virtualServer", "ok")
	state.MarkReady()

	rec = httptest.NewRecorder()
	state.ReadyHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after ready, got %d", rec.Code)
	}

	state.MarkShuttingDown()
	rec = httptest.NewRecorder()
	state.ReadyHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 during shutdown, got %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &notReady); err != nil {
		t.Fatalf("unable to decode shutdown body: %v", err)
	}
	if notReady.Status != "not_ready" {
		t.Fatalf("expected not_ready status during shutdown, got %q", notReady.Status)
	}
}

func TestReadyEndpointRejectsPost(t *testing.T) {
	state := newAppRuntimeState()
	req := httptest.NewRequest(http.MethodPost, "/ready", nil)
	rec := httptest.NewRecorder()
	state.ReadyHandler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestReadyEndpointRequiresAllEnabledChecks(t *testing.T) {
	runtimeConfig = RuntimeConfig{
		TeamlancerMode:     true,
		EnableWeb:          true,
		EnableRawMumbleTCP: true,
	}
	state := newAppRuntimeState()
	state.MarkReady()
	state.SetCheck("dataDirectory", "ok")
	state.SetCheck("voiceEngine", "ok")
	state.SetCheck("auth", "ok")
	state.SetCheck("webListener", "ok")
	state.SetCheck("websocket", "ok")
	state.SetCheck("udp", "disabled")
	state.SetCheck("virtualServer", "ok")

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	state.ReadyHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when raw listener check missing, got %d", rec.Code)
	}
}

func TestReadyEndpointSkipsDisabledRawListenerRequirement(t *testing.T) {
	runtimeConfig = RuntimeConfig{
		TeamlancerMode:     true,
		EnableWeb:          true,
		EnableRawMumbleTCP: false,
	}
	state := newAppRuntimeState()
	state.MarkReady()
	state.SetCheck("dataDirectory", "ok")
	state.SetCheck("voiceEngine", "ok")
	state.SetCheck("auth", "ok")
	state.SetCheck("webListener", "ok")
	state.SetCheck("websocket", "ok")
	state.SetCheck("udp", "disabled")
	state.SetCheck("virtualServer", "ok")

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	state.ReadyHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with raw listener disabled, got %d", rec.Code)
	}
}

func TestReadyEndpointBodyIncludesChecks(t *testing.T) {
	runtimeConfig = RuntimeConfig{
		TeamlancerMode:     true,
		EnableWeb:          true,
		EnableRawMumbleTCP: false,
	}
	state := newAppRuntimeState()
	state.MarkReady()
	state.SetCheck("dataDirectory", "ok")
	state.SetCheck("voiceEngine", "ok")
	state.SetCheck("auth", "ok")
	state.SetCheck("webListener", "ok")
	state.SetCheck("websocket", "ok")
	state.SetCheck("udp", "disabled")
	state.SetCheck("virtualServer", "ok")

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	state.ReadyHandler(rec, req)

	var payload struct {
		Status      string            `json:"status"`
		VoiceEngine string            `json:"voiceEngine"`
		WebSocket   string            `json:"websocket"`
		Auth        string            `json:"auth"`
		Checks      map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unable to decode body: %v", err)
	}
	if payload.Status != "ready" {
		t.Fatalf("expected ready status, got %q", payload.Status)
	}
	if payload.VoiceEngine != "ok" || payload.WebSocket != "ok" || payload.Auth != "ok" {
		t.Fatalf("expected ready voice checks to be ok, got %+v", payload)
	}
	if payload.Checks["udp"] != "disabled" {
		t.Fatalf("expected udp=disabled, got %q", payload.Checks["udp"])
	}
}

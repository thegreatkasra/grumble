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

	state.SetCheck("dataDirectory", "ok")
	state.SetCheck("webListener", "ok")
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
	state.SetCheck("webListener", "ok")
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
	state.SetCheck("webListener", "ok")
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
	state.SetCheck("webListener", "ok")
	state.SetCheck("udp", "disabled")
	state.SetCheck("virtualServer", "ok")

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	state.ReadyHandler(rec, req)

	var payload struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unable to decode body: %v", err)
	}
	if payload.Status != "ready" {
		t.Fatalf("expected ready status, got %q", payload.Status)
	}
	if payload.Checks["udp"] != "disabled" {
		t.Fatalf("expected udp=disabled, got %q", payload.Checks["udp"])
	}
}

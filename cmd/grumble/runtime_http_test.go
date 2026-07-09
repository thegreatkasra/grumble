package main

import (
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
	state.SetCheck("virtualServer", "ok")
	state.MarkReady()

	rec = httptest.NewRecorder()
	state.ReadyHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after ready, got %d", rec.Code)
	}
}

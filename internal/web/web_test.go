package web

import (
	"bytes"
	"encoding/json"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mockcam/internal/auth"
	"mockcam/internal/config"
	"mockcam/internal/onvif"
)

func setupTestWeb(t *testing.T) (*Server, *APIHandler, *config.Manager) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "settings.json")
	cfgMgr, err := config.NewManager(cfgPath)
	if err != nil {
		t.Fatalf("failed to init config: %v", err)
	}

	authenticator := auth.NewAuthenticator(cfgMgr)
	ptz := onvif.NewPTZController(cfgMgr)
	onvifSrv := onvif.NewServer(cfgMgr, authenticator, ptz)

	server := NewServer(cfgMgr, authenticator, nil, nil, ptz, onvifSrv)
	return server, server.apiHandler, cfgMgr
}

func TestWebEndpoints(t *testing.T) {
	_, handler, _ := setupTestWeb(t)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// 1. /api/status
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/status", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var status map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("failed to parse status JSON: %v", err)
	}
	if status["profiles_count"] != float64(2) {
		t.Errorf("expected 2 profiles, got %v", status["profiles_count"])
	}

	// 2. /api/snapshot/Profile_1
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/snapshot/Profile_1", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("expected image/jpeg, got %s", rec.Header().Get("Content-Type"))
	}
	// Verify valid JPEG
	_, err := jpeg.Decode(rec.Body)
	if err != nil {
		t.Fatalf("failed to decode snapshot JPEG: %v", err)
	}

	// 3. /api/ptz
	rec = httptest.NewRecorder()
	ptzBody := []byte(`{"action":"absolute","pan":0.5,"tilt":-0.2,"zoom":0.3}`)
	req = httptest.NewRequest("POST", "/api/ptz", bytes.NewReader(ptzBody))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

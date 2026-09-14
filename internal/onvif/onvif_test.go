package onvif

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mockcam/internal/auth"
	"mockcam/internal/config"
)

func setupTestServer(t *testing.T) (*Server, *PTZController, *config.Manager) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "settings.json")
	cfgMgr, err := config.NewManager(cfgPath)
	if err != nil {
		t.Fatalf("failed to init config: %v", err)
	}

	authenticator := auth.NewAuthenticator(cfgMgr)
	ptz := NewPTZController(cfgMgr)
	srv := NewServer(cfgMgr, authenticator, ptz)
	return srv, ptz, cfgMgr
}

func TestDeviceInformationSOAP(t *testing.T) {
	srv, _, _ := setupTestServer(t)

	soapReq := `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body>
    <GetDeviceInformation xmlns="http://www.onvif.org/ver10/device/wsdl"/>
  </s:Body>
</s:Envelope>`

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/onvif/device_service", bytes.NewBufferString(soapReq))
	// auth is digest/basic by default; set basic header
	req.SetBasicAuth("admin", "admin1234")

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "MC-Pro-S") {
		t.Errorf("expected MC-Pro-S in response, got: %s", body)
	}
	if !strings.Contains(body, "MockCam Standard") {
		t.Errorf("expected MockCam Standard in response, got: %s", body)
	}
}

func TestMediaProfilesAndStreamUri(t *testing.T) {
	srv, _, _ := setupTestServer(t)

	// 1. GetProfiles
	getProfilesReq := `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body>
    <GetProfiles xmlns="http://www.onvif.org/ver10/media/wsdl"/>
  </s:Body>
</s:Envelope>`

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/onvif/media_service", bytes.NewBufferString(getProfilesReq))
	req.SetBasicAuth("admin", "admin1234")

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, `token="Profile_1"`) || !strings.Contains(body, `token="Profile_2"`) {
		t.Errorf("expected Profile_1 and Profile_2 in response: %s", body)
	}

	// 2. GetStreamUri
	getStreamUriReq := `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body>
    <GetStreamUri xmlns="http://www.onvif.org/ver10/media/wsdl">
      <ProfileToken>Profile_1</ProfileToken>
    </GetStreamUri>
  </s:Body>
</s:Envelope>`

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/onvif/media_service", bytes.NewBufferString(getStreamUriReq))
	req.SetBasicAuth("admin", "admin1234")

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	streamBody := rec.Body.String()
	if !strings.Contains(streamBody, "rtsp://") || !strings.Contains(streamBody, "/live/Profile_1") {
		t.Errorf("expected rtsp url for Profile_1: %s", streamBody)
	}
}

func TestPTZStateMachine(t *testing.T) {
	_, ptz, _ := setupTestServer(t)

	// Test AbsoluteMove
	ptz.AbsoluteMove(0.5, -0.7, 0.3)
	pan, tilt, zoom, moving := ptz.GetStatus()
	if pan != 0.5 || tilt != -0.7 || zoom != 0.3 || moving {
		t.Errorf("unexpected PTZ status after AbsoluteMove: %f, %f, %f, %v", pan, tilt, zoom, moving)
	}

	// Test ContinuousMove and Stop
	ptz.ContinuousMove(1.0, 1.0, 0.5)
	_, _, _, moving = ptz.GetStatus()
	if !moving {
		t.Fatal("expected PTZ to be moving")
	}

	time.Sleep(250 * time.Millisecond)
	ptz.Stop()

	panAfter, _, _, movingAfter := ptz.GetStatus()
	if movingAfter {
		t.Fatal("expected PTZ to stop moving")
	}
	if panAfter <= 0.5 {
		t.Errorf("expected pan to have increased from 0.5, got %f", panAfter)
	}

	ptz.WaitSync()
}

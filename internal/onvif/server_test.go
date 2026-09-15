package onvif

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func soapEnvelope(body string) string {
	return `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tt="http://www.onvif.org/ver10/schema">
  <s:Body>` + body + `</s:Body>
</s:Envelope>`
}

// postSOAP sends body to path with basic credentials and returns the recorder.
func postSOAP(t *testing.T, srv *Server, path, body string, withAuth bool) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	req := httptest.NewRequest("POST", path, bytes.NewBufferString(body))
	req.Host = "camera.local:8080"
	if withAuth {
		req.SetBasicAuth("admin", "admin1234")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestReadSOAPRequestActionDetection(t *testing.T) {
	srv, _, _ := setupTestServer(t)

	// 1. The first Body child element wins, even when a (possibly malformed)
	// SOAPAction header is present.
	req := httptest.NewRequest("POST", "/onvif/device_service", strings.NewReader(soapEnvelope(`<tds:GetScopes xmlns:tds="x"/>`)))
	req.Header.Set("SOAPAction", `"http://www.onvif.org/ver10/device/wsdl/GetDeviceInformation"`)
	_, action, err := srv.readSOAPRequest(req)
	if err != nil || action != "GetScopes" {
		t.Fatalf("body wins: action=%q err=%v", action, err)
	}

	// 2. Without a body element the SOAPAction header is used, with or
	// without quotes and with a trailing slash (onvif-zeep sends
	// ".../wsdlGetVideoSources/" for some operations).
	for header, want := range map[string]string{
		`"http://www.onvif.org/ver10/device/wsdl/GetDeviceInformation"`: "GetDeviceInformation",
		`http://www.onvif.org/ver10/media/wsdl/GetProfiles/`:            "GetProfiles",
		`GetScopes`: "GetScopes",
	} {
		req = httptest.NewRequest("POST", "/onvif/device_service", strings.NewReader(""))
		req.Header.Set("SOAPAction", header)
		_, action, err = srv.readSOAPRequest(req)
		if err != nil || action != want {
			t.Fatalf("SOAPAction %q: action=%q err=%v", header, action, err)
		}
	}

	// 3. Empty body and no header yields an empty action (→ ActionNotSupported fault).
	req = httptest.NewRequest("POST", "/onvif/device_service", strings.NewReader(""))
	_, action, err = srv.readSOAPRequest(req)
	if err != nil || action != "" {
		t.Fatalf("empty body: action=%q err=%v", action, err)
	}
}

func TestParsePTZCoords(t *testing.T) {
	body := []byte(`<ContinuousMove><ProfileToken>P</ProfileToken>
		<Velocity><PanTilt x="0.5" y="-0.25"/><Zoom x="0.75"/></Velocity></ContinuousMove>`)
	p, ti, z := parsePTZCoords(body, "Velocity")
	if p != 0.5 || ti != -0.25 || z != 0.75 {
		t.Fatalf("got %v %v %v", p, ti, z)
	}
	// Elements outside the parent are ignored; missing parent yields zeros.
	p, ti, z = parsePTZCoords(body, "Position")
	if p != 0 || ti != 0 || z != 0 {
		t.Fatalf("wrong parent should yield zeros, got %v %v %v", p, ti, z)
	}
	// Namespaced elements work as well.
	nsBody := []byte(`<tptz:AbsoluteMove xmlns:tptz="a" xmlns:tt="b"><tptz:Position><tt:PanTilt x="1" y="1"/><tt:Zoom x="0.1"/></tptz:Position></tptz:AbsoluteMove>`)
	if p, ti, z = parsePTZCoords(nsBody, "Position"); p != 1 || ti != 1 || z != 0.1 {
		t.Fatalf("namespaced: %v %v %v", p, ti, z)
	}
	if p, _, _ = parsePTZCoords([]byte("<broken"), "Position"); p != 0 {
		t.Fatal("broken XML should not panic")
	}
}

func TestDeviceServiceAuthRules(t *testing.T) {
	srv, _, _ := setupTestServer(t)

	// GetSystemDateAndTime and GetCapabilities are reachable without credentials (ONVIF requirement).
	for _, action := range []string{"GetSystemDateAndTime", "GetCapabilities"} {
		rec := postSOAP(t, srv, "/onvif/device_service", soapEnvelope(fmt.Sprintf(`<tds:%s xmlns:tds="http://www.onvif.org/ver10/device/wsdl"/>`, action)), false)
		if rec.Code != 200 {
			t.Fatalf("%s without auth: %d", action, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), action+"Response") {
			t.Fatalf("%s: missing response element: %s", action, rec.Body.String())
		}
	}

	// GetDeviceInformation requires credentials.
	rec := postSOAP(t, srv, "/onvif/device_service", soapEnvelope(`<tds:GetDeviceInformation xmlns:tds="http://www.onvif.org/ver10/device/wsdl"/>`), false)
	if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("expected 401 with challenge, got %d %v", rec.Code, rec.Header())
	}

	// GET is not allowed.
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, httptest.NewRequest("GET", "/onvif/device_service", nil))
	if getRec.Code != 405 {
		t.Fatalf("GET: %d", getRec.Code)
	}
}

func TestDeviceServiceCapabilitiesUseRequestHost(t *testing.T) {
	srv, _, _ := setupTestServer(t)
	rec := postSOAP(t, srv, "/onvif/device_service", soapEnvelope(`<tds:GetCapabilities xmlns:tds="http://www.onvif.org/ver10/device/wsdl"/>`), false)
	body := rec.Body.String()
	for _, want := range []string{"http://camera.local:8080/onvif/device_service", "http://camera.local:8080/onvif/media_service", "http://camera.local:8080/onvif/ptz_service"} {
		if !strings.Contains(body, want) {
			t.Errorf("capabilities missing %s", want)
		}
	}
}

func TestUnsupportedActionReturnsFault(t *testing.T) {
	srv, _, _ := setupTestServer(t)
	for _, path := range []string{"/onvif/device_service", "/onvif/media_service", "/onvif/ptz_service"} {
		rec := postSOAP(t, srv, path, soapEnvelope(`<Bogus/>`), true)
		if rec.Code != 500 || !strings.Contains(rec.Body.String(), "ActionNotSupported") {
			t.Errorf("%s: expected ActionNotSupported fault, got %d %s", path, rec.Code, rec.Body.String())
		}
	}
	// Malformed XML without SOAPAction → Sender fault (empty action is unsupported).
	rec := postSOAP(t, srv, "/onvif/ptz_service", "<not-xml", true)
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "s:Fault") {
		t.Fatalf("malformed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPTZServiceRoundTrip(t *testing.T) {
	srv, ptz, cfgMgr := setupTestServer(t)
	t.Cleanup(ptz.WaitSync)

	// AbsoluteMove
	rec := postSOAP(t, srv, "/onvif/ptz_service", soapEnvelope(`<tptz:AbsoluteMove xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"><tptz:ProfileToken>Profile_1</tptz:ProfileToken><tptz:Position><tt:PanTilt x="0.5" y="-0.5"/><tt:Zoom x="0.25"/></tptz:Position></tptz:AbsoluteMove>`), true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "AbsoluteMoveResponse") {
		t.Fatalf("AbsoluteMove: %d %s", rec.Code, rec.Body.String())
	}
	if p, ti, z, moving := ptz.GetStatus(); p != 0.5 || ti != -0.5 || z != 0.25 || moving {
		t.Fatalf("state after AbsoluteMove = %v %v %v %v", p, ti, z, moving)
	}

	// GetStatus reflects the position and IDLE state.
	rec = postSOAP(t, srv, "/onvif/ptz_service", soapEnvelope(`<tptz:GetStatus xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"><tptz:ProfileToken>Profile_1</tptz:ProfileToken></tptz:GetStatus>`), true)
	body := rec.Body.String()
	if !strings.Contains(body, `x="0.5000" y="-0.5000"`) || !strings.Contains(body, "<tt:PanTilt>IDLE</tt:PanTilt>") {
		t.Fatalf("GetStatus: %s", body)
	}
	// The response must be well-formed XML.
	if err := xml.Unmarshal(rec.Body.Bytes(), new(struct{})); err != nil {
		t.Fatalf("GetStatus response is not valid XML: %v", err)
	}

	// ContinuousMove sets MOVING, Stop clears it.
	rec = postSOAP(t, srv, "/onvif/ptz_service", soapEnvelope(`<tptz:ContinuousMove xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"><tptz:Velocity><tt:PanTilt x="1" y="0"/></tptz:Velocity></tptz:ContinuousMove>`), true)
	if rec.Code != 200 {
		t.Fatalf("ContinuousMove: %d", rec.Code)
	}
	rec = postSOAP(t, srv, "/onvif/ptz_service", soapEnvelope(`<tptz:GetStatus xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"/>`), true)
	if !strings.Contains(rec.Body.String(), "<tt:PanTilt>MOVING</tt:PanTilt>") {
		t.Fatalf("expected MOVING: %s", rec.Body.String())
	}
	rec = postSOAP(t, srv, "/onvif/ptz_service", soapEnvelope(`<tptz:Stop xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"/>`), true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "StopResponse") {
		t.Fatalf("Stop: %d", rec.Code)
	}
	if _, _, _, moving := ptz.GetStatus(); moving {
		t.Fatal("Stop should clear moving")
	}

	// GetNodes / GetConfigurations expose the configured node token.
	nodeToken := cfgMgr.Get().PTZ.NodeToken
	rec = postSOAP(t, srv, "/onvif/ptz_service", soapEnvelope(`<tptz:GetNodes xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"/>`), true)
	if !strings.Contains(rec.Body.String(), `token="`+nodeToken+`"`) {
		t.Fatalf("GetNodes: %s", rec.Body.String())
	}
	rec = postSOAP(t, srv, "/onvif/ptz_service", soapEnvelope(`<tptz:GetConfigurations xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"/>`), true)
	if !strings.Contains(rec.Body.String(), "<tt:NodeToken>"+nodeToken+"</tt:NodeToken>") {
		t.Fatalf("GetConfigurations: %s", rec.Body.String())
	}

	// PTZ service always requires auth.
	rec = postSOAP(t, srv, "/onvif/ptz_service", soapEnvelope(`<tptz:GetStatus xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"/>`), false)
	if rec.Code != 401 {
		t.Fatalf("PTZ without auth: %d", rec.Code)
	}
}

func TestMediaServiceEncoderAndSources(t *testing.T) {
	srv, _, _ := setupTestServer(t)
	rec := postSOAP(t, srv, "/onvif/media_service", soapEnvelope(`<trt:GetVideoEncoderConfigurations xmlns:trt="http://www.onvif.org/ver10/media/wsdl"/>`), true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "VEC_Profile_1") || !strings.Contains(rec.Body.String(), "VEC_Profile_2") {
		t.Fatalf("encoder configurations: %d %s", rec.Code, rec.Body.String())
	}
	rec = postSOAP(t, srv, "/onvif/media_service", soapEnvelope(`<trt:GetVideoSources xmlns:trt="http://www.onvif.org/ver10/media/wsdl"/>`), true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "VideoSource") {
		t.Fatalf("video sources: %d %s", rec.Code, rec.Body.String())
	}
	rec = postSOAP(t, srv, "/onvif/media_service", soapEnvelope(`<trt:GetSnapshotUri xmlns:trt="http://www.onvif.org/ver10/media/wsdl"><trt:ProfileToken>Profile_2</trt:ProfileToken></trt:GetSnapshotUri>`), true)
	if !strings.Contains(rec.Body.String(), "http://camera.local:8080/api/snapshot/Profile_2") {
		t.Fatalf("snapshot uri: %s", rec.Body.String())
	}
	// Media service requires auth.
	if rec := postSOAP(t, srv, "/onvif/media_service", soapEnvelope(`<trt:GetProfiles xmlns:trt="x"/>`), false); rec.Code != 401 {
		t.Fatalf("media without auth: %d", rec.Code)
	}
}

func TestWSDiscoveryProbeHelpers(t *testing.T) {
	probe := []byte(`<?xml version="1.0"?><e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope" xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery">
<e:Header><w:MessageID>urn:uuid:1234-abcd</w:MessageID><w:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</w:To><w:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</w:Action></e:Header>
<e:Body><d:Probe><d:Types>dn:NetworkVideoTransmitter</d:Types></d:Probe></e:Body></e:Envelope>`)

	if !IsProbe(probe) {
		t.Fatal("valid probe not recognised")
	}
	if IsProbe([]byte("hello")) || IsProbe([]byte("<Hello xmlns=\"discovery\"/>")) {
		t.Fatal("non-probe datagrams must be ignored")
	}
	if got := ProbeMessageID(probe); got != "urn:uuid:1234-abcd" {
		t.Fatalf("message id = %q", got)
	}
	if got := ProbeMessageID([]byte("<garbage")); !strings.HasPrefix(got, "urn:uuid:") {
		t.Fatalf("fallback message id = %q", got)
	}

	_, _, cfgMgr := setupTestServer(t)
	cfg := cfgMgr.Get()
	out, err := BuildProbeMatches(probe, "192.168.10.5", cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "<?xml") {
		t.Fatal("missing XML declaration")
	}
	for _, want := range []string{
		"http://192.168.10.5:8080/onvif/device_service",
		"<wsa:RelatesTo>urn:uuid:1234-abcd</wsa:RelatesTo>",
		"onvif://www.onvif.org/hardware/" + cfg.Server.DeviceInfo.Model,
		"<wsa:Address>urn:uuid:" + DeviceUUID(cfg.Server.DeviceInfo.SerialNumber) + "</wsa:Address>",
		"dn:NetworkVideoTransmitter",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("ProbeMatches missing %q:\n%s", want, s)
		}
	}
	if err := xml.Unmarshal(out, new(struct{})); err != nil {
		t.Fatalf("ProbeMatches is not valid XML: %v", err)
	}
}

func TestPTZControllerClampAndPresetsPersist(t *testing.T) {
	_, ptz, cfgMgr := setupTestServer(t)
	t.Cleanup(ptz.WaitSync)

	ptz.AbsoluteMove(5, -5, 9)
	if p, ti, z, _ := ptz.GetStatus(); p != 1 || ti != -1 || z != 1 {
		t.Fatalf("values not clamped: %v %v %v", p, ti, z)
	}
	ptz.WaitSync()
	if got := cfgMgr.Get().PTZ; got.Pan != 1 || got.Tilt != -1 || got.Zoom != 1 {
		t.Fatalf("position not persisted: %+v", got)
	}

	// A new controller starts from the persisted position.
	again := NewPTZController(cfgMgr)
	if p, _, z, _ := again.GetStatus(); p != 1 || z != 1 {
		t.Fatal("controller should initialise from config")
	}
}

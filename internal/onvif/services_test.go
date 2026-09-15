package onvif

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mockcam/internal/auth"
	"mockcam/internal/config"
)

const (
	nsDevice = "http://www.onvif.org/ver10/device/wsdl"
	nsMedia  = "http://www.onvif.org/ver10/media/wsdl"
	nsPTZ    = "http://www.onvif.org/ver20/ptz/wsdl"
)

// wsseEnvelope wraps body in an envelope carrying a WS-UsernameToken
// PasswordDigest header computed for pass at the given time.
func wsseEnvelope(user, pass string, created time.Time, body string) string {
	nonce := []byte("mockcam-test-nonce")
	createdStr := created.UTC().Format("2006-01-02T15:04:05Z")
	h := sha1.New()
	h.Write(nonce)
	h.Write([]byte(createdStr))
	h.Write([]byte(pass))
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tt="http://www.onvif.org/ver10/schema">
  <s:Header>
    <wsse:Security xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">
      <wsse:UsernameToken>
        <wsse:Username>%s</wsse:Username>
        <wsse:Password Type="%s">%s</wsse:Password>
        <wsse:Nonce>%s</wsse:Nonce>
        <wsu:Created xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">%s</wsu:Created>
      </wsse:UsernameToken>
    </wsse:Security>
  </s:Header>
  <s:Body>%s</s:Body>
</s:Envelope>`, user, auth.PasswordDigestType, base64.StdEncoding.EncodeToString(h.Sum(nil)),
		base64.StdEncoding.EncodeToString(nonce), createdStr, body)
}

// postRaw posts body without HTTP credentials.
func postRaw(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postSOAP(t, srv, path, body, false)
}

// assertResponse checks status 200, that the body is well-formed XML, contains
// the response element and has no fmt formatting accidents.
func assertResponse(t *testing.T, rec *httptest.ResponseRecorder, action string) string {
	t.Helper()
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", action, rec.Code, body)
	}
	if !strings.Contains(body, action+"Response") {
		t.Fatalf("%s: missing response element: %s", action, body)
	}
	if strings.Contains(body, "%!") {
		t.Fatalf("%s: formatting error in response: %s", action, body)
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), new(struct{})); err != nil {
		t.Fatalf("%s: response is not well-formed XML: %v\n%s", action, err, body)
	}
	return body
}

func assertFault(t *testing.T, rec *httptest.ResponseRecorder, status int, subcode string) string {
	t.Helper()
	body := rec.Body.String()
	if rec.Code != status {
		t.Fatalf("expected status %d, got %d: %s", status, rec.Code, body)
	}
	for _, code := range strings.Split(subcode, "/") {
		if !strings.Contains(body, "<s:Value>"+code+"</s:Value>") {
			t.Fatalf("expected subcode %s in fault: %s", code, body)
		}
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), new(struct{})); err != nil {
		t.Fatalf("fault is not well-formed XML: %v", err)
	}
	return body
}

func TestWSUsernameTokenAuthentication(t *testing.T) {
	srv, _, _ := setupTestServer(t)
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	srv.now = func() time.Time { return now }
	req := `<tds:GetDeviceInformation xmlns:tds="` + nsDevice + `"/>`

	// A valid PasswordDigest token authenticates a device call.
	rec := postRaw(t, srv, "/onvif/device_service", wsseEnvelope("admin", "admin1234", now, req))
	body := assertResponse(t, rec, "GetDeviceInformation")
	if !strings.Contains(body, "<tds:Manufacturer>") {
		t.Fatalf("unexpected body: %s", body)
	}

	// ... and media / PTZ calls too.
	rec = postRaw(t, srv, "/onvif/media_service", wsseEnvelope("admin", "admin1234", now, `<trt:GetProfiles xmlns:trt="`+nsMedia+`"/>`))
	assertResponse(t, rec, "GetProfiles")
	rec = postRaw(t, srv, "/onvif/ptz_service", wsseEnvelope("admin", "admin1234", now, `<tptz:GetStatus xmlns:tptz="`+nsPTZ+`"><tptz:ProfileToken>Profile_1</tptz:ProfileToken></tptz:GetStatus>`))
	assertResponse(t, rec, "GetStatus")

	// Wrong password → 400 + ter:NotAuthorized fault (no HTTP challenge needed).
	rec = postRaw(t, srv, "/onvif/device_service", wsseEnvelope("admin", "wrong", now, req))
	assertFault(t, rec, http.StatusBadRequest, "ter:NotAuthorized")

	// Token outside the allowed clock skew is rejected.
	rec = postRaw(t, srv, "/onvif/device_service", wsseEnvelope("admin", "admin1234", now.Add(-auth.UsernameTokenMaxSkew-time.Minute), req))
	assertFault(t, rec, http.StatusBadRequest, "ter:NotAuthorized")

	// No credentials at all → 401 with a challenge AND a SOAP fault body.
	rec = postRaw(t, srv, "/onvif/device_service", soapEnvelope(req))
	assertFault(t, rec, http.StatusUnauthorized, "ter:NotAuthorized")
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("missing WWW-Authenticate challenge")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/soap+xml") {
		t.Fatalf("fault content type = %q", ct)
	}

	// HTTP Basic still works when the header is present.
	rec = postSOAP(t, srv, "/onvif/device_service", soapEnvelope(req), true)
	assertResponse(t, rec, "GetDeviceInformation")
}

func TestDeviceServiceActions(t *testing.T) {
	srv, _, _ := setupTestServer(t)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	call := func(action string) string {
		t.Helper()
		req := httptest.NewRequest("POST", "/onvif/device_service", strings.NewReader(soapEnvelope(`<tds:`+action+` xmlns:tds="`+nsDevice+`"/>`)))
		req.Host = "192.168.10.5:8080"
		req.SetBasicAuth("admin", "admin1234")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return assertResponse(t, rec, action)
	}

	checks := map[string][]string{
		"GetDeviceInformation":   {"<tds:Manufacturer>MockCam Standard</tds:Manufacturer>", "<tds:Model>MC-Pro-S</tds:Model>"},
		"GetSystemDateAndTime":   {"<tt:UTCDateTime>", "<tt:Year>"},
		"GetCapabilities":        {"http://192.168.10.5:8080/onvif/media_service", "<tt:PTZ>"},
		"GetServices":            {nsDevice, nsMedia, nsPTZ},
		"GetServiceCapabilities": {`UsernameToken="true"`, `HttpDigest="true"`},
		"GetScopes":              {"onvif://www.onvif.org/type/NetworkVideoTransmitter", "onvif://www.onvif.org/hardware/MC-Pro-S"},
		"GetHostname":            {"<tt:Name>MockCam</tt:Name>"},
		"GetNetworkInterfaces":   {`token="eth0"`, "<tt:Address>192.168.10.5</tt:Address>", "<tt:HwAddress>"},
		"GetNetworkProtocols":    {"<tt:Name>HTTP</tt:Name>", "<tt:Port>8080</tt:Port>", "<tt:Name>RTSP</tt:Name>", "<tt:Port>8554</tt:Port>"},
		"GetDNS":                 {"<tt:FromDHCP>false</tt:FromDHCP>"},
		"GetNTP":                 {"<tds:NTPInformation>"},
		"GetDiscoveryMode":       {"<tds:DiscoveryMode>Discoverable</tds:DiscoveryMode>"},
		"GetUsers":               {"<tt:Username>admin</tt:Username>", "<tt:UserLevel>Administrator</tt:UserLevel>"},
		"GetWsdlUrl":             {"<tds:WsdlUrl>"},
	}
	for action, wants := range checks {
		body := call(action)
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q in\n%s", action, want, body)
			}
		}
	}
	if strings.Contains(call("GetUsers"), "admin1234") {
		t.Fatal("GetUsers must not leak the password")
	}
}

func TestPTZDisabledHidesPTZService(t *testing.T) {
	// Start from a settings.json with ptz.enabled=false.
	cfg := config.DefaultConfig()
	cfg.PTZ.Enabled = false
	raw, _ := json.Marshal(cfg)
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	cfgMgr, err := config.NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfgMgr, auth.NewAuthenticator(cfgMgr), NewPTZController(cfgMgr))

	device := func(action string) string {
		t.Helper()
		return assertResponse(t, postSOAP(t, srv, "/onvif/device_service", soapEnvelope(`<tds:`+action+` xmlns:tds="`+nsDevice+`"/>`), true), action)
	}
	if body := device("GetCapabilities"); strings.Contains(body, "<tt:PTZ>") {
		t.Fatalf("PTZ capability advertised while disabled: %s", body)
	}
	if body := device("GetServices"); strings.Contains(body, nsPTZ) {
		t.Fatalf("PTZ service listed while disabled: %s", body)
	}
	body := assertResponse(t, postSOAP(t, srv, "/onvif/media_service", soapEnvelope(`<trt:GetProfiles xmlns:trt="`+nsMedia+`"/>`), true), "GetProfiles")
	if strings.Contains(body, "PTZConfiguration") {
		t.Fatalf("profiles carry a PTZConfiguration while PTZ is disabled: %s", body)
	}
}

func TestMediaServiceActions(t *testing.T) {
	srv, _, _ := setupTestServer(t)
	media := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		return postSOAP(t, srv, "/onvif/media_service", soapEnvelope(body), true)
	}

	// GetProfile returns the single addressed profile including the shared PTZ configuration.
	body := assertResponse(t, media(`<trt:GetProfile xmlns:trt="`+nsMedia+`"><trt:ProfileToken>Profile_2</trt:ProfileToken></trt:GetProfile>`), "GetProfile")
	for _, want := range []string{`<trt:Profile token="Profile_2" fixed="true">`, `token="VSC_Profile_2"`, `token="VEC_Profile_2"`, `<tt:PTZConfiguration token="` + ptzConfigurationToken + `">`, "<tt:Encoding>H264</tt:Encoding>", "<tt:Quality>"} {
		if !strings.Contains(body, want) {
			t.Errorf("GetProfile: missing %q in\n%s", want, body)
		}
	}
	if strings.Contains(body, `token="Profile_1"`) {
		t.Fatal("GetProfile must not return other profiles")
	}

	// Unknown profile tokens are faults, not silent fallbacks.
	assertFault(t, media(`<trt:GetProfile xmlns:trt="`+nsMedia+`"><trt:ProfileToken>nope</trt:ProfileToken></trt:GetProfile>`), http.StatusBadRequest, "ter:InvalidArgVal/ter:NoProfile")
	assertFault(t, media(`<trt:GetStreamUri xmlns:trt="`+nsMedia+`"><trt:ProfileToken>nope</trt:ProfileToken></trt:GetStreamUri>`), http.StatusBadRequest, "ter:InvalidArgVal/ter:NoProfile")
	assertFault(t, media(`<trt:GetSnapshotUri xmlns:trt="`+nsMedia+`"><trt:ProfileToken>nope</trt:ProfileToken></trt:GetSnapshotUri>`), http.StatusBadRequest, "ter:InvalidArgVal/ter:NoProfile")
	assertFault(t, media(`<trt:GetVideoEncoderConfiguration xmlns:trt="`+nsMedia+`"><trt:ConfigurationToken>VEC_nope</trt:ConfigurationToken></trt:GetVideoEncoderConfiguration>`), http.StatusBadRequest, "ter:InvalidArgVal/ter:NoConfig")

	checks := map[string][]string{
		`<trt:GetVideoSourceConfigurations xmlns:trt="` + nsMedia + `"/>`:                                                                                                                      {`token="VSC_Profile_1"`, `token="VSC_Profile_2"`, "<tt:SourceToken>VideoSource_1</tt:SourceToken>"},
		`<trt:GetVideoSourceConfiguration xmlns:trt="` + nsMedia + `"><trt:ConfigurationToken>VSC_Profile_1</trt:ConfigurationToken></trt:GetVideoSourceConfiguration>`:                        {`<trt:Configuration token="VSC_Profile_1">`},
		`<trt:GetVideoEncoderConfiguration xmlns:trt="` + nsMedia + `"><trt:ConfigurationToken>VEC_Profile_1</trt:ConfigurationToken></trt:GetVideoEncoderConfiguration>`:                      {`<trt:Configuration token="VEC_Profile_1">`, "<tt:GovLength>"},
		`<trt:GetVideoEncoderConfigurationOptions xmlns:trt="` + nsMedia + `"><trt:ProfileToken>Profile_1</trt:ProfileToken></trt:GetVideoEncoderConfigurationOptions>`:                        {"<tt:ResolutionsAvailable><tt:Width>1920</tt:Width>", "<tt:H264ProfilesSupported>"},
		`<trt:GetAudioSources xmlns:trt="` + nsMedia + `"/>`:                                                                                                                                   {`<trt:AudioSources token="AudioSource_1">`},
		`<trt:GetAudioSourceConfigurations xmlns:trt="` + nsMedia + `"/>`:                                                                                                                      {`token="ASC_Profile_1"`},
		`<trt:GetAudioEncoderConfigurations xmlns:trt="` + nsMedia + `"/>`:                                                                                                                     {`token="AEC_Profile_1"`, "<tt:Encoding>AAC</tt:Encoding>"},
		`<trt:GetAudioEncoderConfiguration xmlns:trt="` + nsMedia + `"><trt:ConfigurationToken>AEC_Profile_2</trt:ConfigurationToken></trt:GetAudioEncoderConfiguration>`:                      {`<trt:Configuration token="AEC_Profile_2">`},
		`<trt:GetServiceCapabilities xmlns:trt="` + nsMedia + `"/>`:                                                                                                                            {`SnapshotUri="true"`, `RTP_RTSP_TCP="true"`},
		`<trt:GetStreamUri xmlns:trt="` + nsMedia + `"><trt:StreamSetup><tt:Stream>RTP-Unicast</tt:Stream></trt:StreamSetup><trt:ProfileToken>Profile_2</trt:ProfileToken></trt:GetStreamUri>`: {"rtsp://camera.local:8554/live/Profile_2"},
	}
	for req, wants := range checks {
		action := req[strings.Index(req, ":")+1 : strings.IndexAny(req, " />")]
		body := assertResponse(t, media(req), action)
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q in\n%s", action, want, body)
			}
		}
	}
}

func TestMediaEncodingMapping(t *testing.T) {
	for in, want := range map[string]string{"": "H264", "h264": "H264", "H265": "H265", "HEVC": "H265", "MJPEG": "JPEG", "VP9": "VP9"} {
		if got := onvifVideoEncoding(in); got != want {
			t.Errorf("video %q = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"AAC": "AAC", "PCMA": "G711", "G711U": "G711", "MULAW": "G711", "ADPCM_G726": "G726", "": "AAC"} {
		if got := onvifAudioEncoding(in); got != want {
			t.Errorf("audio %q = %q, want %q", in, got, want)
		}
	}
}

func TestPTZServiceExtendedActions(t *testing.T) {
	srv, ptz, cfgMgr := setupTestServer(t)
	t.Cleanup(ptz.WaitSync)
	call := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		return postSOAP(t, srv, "/onvif/ptz_service", soapEnvelope(body), true)
	}
	tptz := func(action, inner string) string {
		return `<tptz:` + action + ` xmlns:tptz="` + nsPTZ + `"><tptz:ProfileToken>Profile_1</tptz:ProfileToken>` + inner + `</tptz:` + action + `>`
	}

	// AbsoluteMove with only Zoom keeps pan/tilt.
	ptz.AbsoluteMove(0.5, 0.5, 0)
	assertResponse(t, call(tptz("AbsoluteMove", `<tptz:Position><tt:Zoom x="0.75"/></tptz:Position>`)), "AbsoluteMove")
	if p, ti, z, _ := ptz.GetStatus(); p != 0.5 || ti != 0.5 || z != 0.75 {
		t.Fatalf("zoom-only AbsoluteMove: %v %v %v", p, ti, z)
	}

	// RelativeMove adds a translation and clamps.
	assertResponse(t, call(tptz("RelativeMove", `<tptz:Translation><tt:PanTilt x="0.25" y="1"/><tt:Zoom x="-0.5"/></tptz:Translation>`)), "RelativeMove")
	if p, ti, z, _ := ptz.GetStatus(); p != 0.75 || ti != 1 || z != 0.25 {
		t.Fatalf("RelativeMove: %v %v %v", p, ti, z)
	}

	// Home: default origin, then SetHomePosition captures the current position.
	assertResponse(t, call(tptz("GotoHomePosition", "")), "GotoHomePosition")
	if p, ti, z, _ := ptz.GetStatus(); p != 0 || ti != 0 || z != 0 {
		t.Fatalf("default home: %v %v %v", p, ti, z)
	}
	ptz.AbsoluteMove(-0.3, 0.3, 0.1)
	assertResponse(t, call(tptz("SetHomePosition", "")), "SetHomePosition")
	ptz.AbsoluteMove(1, 1, 1)
	assertResponse(t, call(tptz("GotoHomePosition", "")), "GotoHomePosition")
	if p, ti, z, _ := ptz.GetStatus(); p != -0.3 || ti != 0.3 || z != 0.1 {
		t.Fatalf("custom home: %v %v %v", p, ti, z)
	}

	// Presets: create by name, list, goto, remove; persisted in settings.json.
	ptz.AbsoluteMove(0.2, -0.2, 0.9)
	body := assertResponse(t, call(tptz("SetPreset", `<tptz:PresetName>Door &amp; Gate</tptz:PresetName>`)), "SetPreset")
	if !strings.Contains(body, "<tptz:PresetToken>Door &amp; Gate</tptz:PresetToken>") {
		t.Fatalf("SetPreset token: %s", body)
	}
	body = assertResponse(t, call(tptz("SetPreset", "")), "SetPreset")
	if !strings.Contains(body, "<tptz:PresetToken>Preset_2</tptz:PresetToken>") {
		t.Fatalf("auto-named preset: %s", body)
	}
	body = assertResponse(t, call(tptz("GetPresets", "")), "GetPresets")
	if !strings.Contains(body, `<tptz:Preset token="Door &amp; Gate">`) || !strings.Contains(body, `x="0.2000" y="-0.2000"`) || !strings.Contains(body, `<tt:Zoom x="0.9000"`) {
		t.Fatalf("GetPresets: %s", body)
	}
	if got := cfgMgr.Get().PTZ.Presets; len(got) != 2 || got[0].Name != "Door & Gate" {
		t.Fatalf("presets not persisted: %+v", got)
	}
	ptz.AbsoluteMove(0, 0, 0)
	assertResponse(t, call(tptz("GotoPreset", `<tptz:PresetToken>Door &amp; Gate</tptz:PresetToken>`)), "GotoPreset")
	if p, ti, z, _ := ptz.GetStatus(); p != 0.2 || ti != -0.2 || z != 0.9 {
		t.Fatalf("GotoPreset: %v %v %v", p, ti, z)
	}
	assertFault(t, call(tptz("GotoPreset", `<tptz:PresetToken>missing</tptz:PresetToken>`)), http.StatusBadRequest, "ter:InvalidArgVal/ter:NoToken")
	assertFault(t, call(tptz("RemovePreset", `<tptz:PresetToken>missing</tptz:PresetToken>`)), http.StatusBadRequest, "ter:InvalidArgVal/ter:NoToken")
	assertResponse(t, call(tptz("RemovePreset", `<tptz:PresetToken>Preset_2</tptz:PresetToken>`)), "RemovePreset")
	if got := cfgMgr.Get().PTZ.Presets; len(got) != 1 {
		t.Fatalf("preset not removed: %+v", got)
	}

	// Node / configuration lookups.
	nodeToken := cfgMgr.Get().PTZ.NodeToken
	body = assertResponse(t, call(`<tptz:GetNode xmlns:tptz="`+nsPTZ+`"><tptz:NodeToken>`+nodeToken+`</tptz:NodeToken></tptz:GetNode>`), "GetNode")
	if !strings.Contains(body, "<tt:ContinuousPanTiltVelocitySpace>") || !strings.Contains(body, "<tt:HomeSupported>true</tt:HomeSupported>") {
		t.Fatalf("GetNode: %s", body)
	}
	assertFault(t, call(`<tptz:GetNode xmlns:tptz="`+nsPTZ+`"><tptz:NodeToken>other</tptz:NodeToken></tptz:GetNode>`), http.StatusBadRequest, "ter:InvalidArgVal/ter:NoEntity")
	body = assertResponse(t, call(`<tptz:GetConfiguration xmlns:tptz="`+nsPTZ+`"><tptz:PTZConfigurationToken>`+ptzConfigurationToken+`</tptz:PTZConfigurationToken></tptz:GetConfiguration>`), "GetConfiguration")
	if !strings.Contains(body, "<tt:NodeToken>"+nodeToken+"</tt:NodeToken>") || !strings.Contains(body, "<tt:DefaultPTZTimeout>") {
		t.Fatalf("GetConfiguration: %s", body)
	}
	body = assertResponse(t, call(`<tptz:GetConfigurationOptions xmlns:tptz="`+nsPTZ+`"><tptz:ConfigurationToken>`+ptzConfigurationToken+`</tptz:ConfigurationToken></tptz:GetConfigurationOptions>`), "GetConfigurationOptions")
	if !strings.Contains(body, "<tt:PTZTimeout>") || !strings.Contains(body, "<tt:Spaces>") {
		t.Fatalf("GetConfigurationOptions: %s", body)
	}
	assertFault(t, call(`<tptz:GetConfigurationOptions xmlns:tptz="`+nsPTZ+`"><tptz:ConfigurationToken>x</tptz:ConfigurationToken></tptz:GetConfigurationOptions>`), http.StatusBadRequest, "ter:InvalidArgVal/ter:NoConfig")
	assertResponse(t, call(`<tptz:GetServiceCapabilities xmlns:tptz="`+nsPTZ+`"/>`), "GetServiceCapabilities")

	// The profile's PTZ configuration token matches GetConfigurations.
	profiles := postSOAP(t, srv, "/onvif/media_service", soapEnvelope(`<trt:GetProfiles xmlns:trt="`+nsMedia+`"/>`), true).Body.String()
	configs := assertResponse(t, call(`<tptz:GetConfigurations xmlns:tptz="`+nsPTZ+`"/>`), "GetConfigurations")
	if !strings.Contains(profiles, `<tt:PTZConfiguration token="`+ptzConfigurationToken+`">`) || !strings.Contains(configs, `<tptz:PTZConfiguration token="`+ptzConfigurationToken+`">`) {
		t.Fatal("PTZ configuration token differs between GetProfiles and GetConfigurations")
	}
}

func TestSOAPFaultShape(t *testing.T) {
	srv, _, _ := setupTestServer(t)
	rec := postSOAP(t, srv, "/onvif/media_service", soapEnvelope(`<trt:Bogus xmlns:trt="`+nsMedia+`"/>`), true)
	body := assertFault(t, rec, http.StatusInternalServerError, "ter:ActionNotSupported")
	if !strings.Contains(body, "<s:Value>s:Receiver</s:Value>") {
		t.Fatalf("unsupported action must be a Receiver fault: %s", body)
	}
	// Nested subcodes are rendered as nested s:Subcode elements (zeep and
	// other SOAP stacks cannot parse a "/"-joined QName).
	rec = postSOAP(t, srv, "/onvif/media_service", soapEnvelope(`<trt:GetProfile xmlns:trt="`+nsMedia+`"><trt:ProfileToken>x</trt:ProfileToken></trt:GetProfile>`), true)
	body = assertFault(t, rec, http.StatusBadRequest, "ter:InvalidArgVal/ter:NoProfile")
	if !strings.Contains(body, "<s:Subcode><s:Value>ter:InvalidArgVal</s:Value><s:Subcode><s:Value>ter:NoProfile</s:Value></s:Subcode></s:Subcode>") {
		t.Fatalf("subcodes not nested: %s", body)
	}
	if strings.Contains(body, "ter:InvalidArgVal/ter:NoProfile") {
		t.Fatalf("joined subcode leaked into the fault: %s", body)
	}
}

func TestProbeMatchesDeclaresTypePrefixes(t *testing.T) {
	_, _, cfgMgr := setupTestServer(t)
	out, err := BuildProbeMatches([]byte("<Probe/>"), "10.0.0.1", cfgMgr.Get())
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`xmlns:dn="` + NamespaceNetworkWSDL + `"`, `xmlns:tds="` + NamespaceDeviceWSDL + `"`} {
		if !strings.Contains(s, want) {
			t.Errorf("ProbeMatches missing %s:\n%s", want, s)
		}
	}
}

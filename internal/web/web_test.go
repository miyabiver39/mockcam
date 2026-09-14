package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"mockcam/internal/auth"
	"mockcam/internal/config"
	"mockcam/internal/frames"
	"mockcam/internal/logger"
	"mockcam/internal/onvif"
	"mockcam/internal/rtsp"
	"mockcam/internal/supervisor"
	"mockcam/internal/timesignal"
)

// ---- fakes ---------------------------------------------------------------

type fakeSupervisor struct {
	mu        sync.Mutex
	restarted []string
	stopped   []string
	status    []supervisor.WorkerStatus
}

func (f *fakeSupervisor) RestartProfile(token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarted = append(f.restarted, token)
	return nil
}
func (f *fakeSupervisor) StopProfile(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, token)
}
func (f *fakeSupervisor) Status() []supervisor.WorkerStatus { return f.status }

type fakeRTSP struct {
	mu     sync.Mutex
	closed []string
}

func (f *fakeRTSP) GetClientCount() int64 { return 3 }
func (f *fakeRTSP) GetStats() rtsp.StreamStats {
	return rtsp.StreamStats{PacketsSent: 10, BytesSent: 2000, BitrateKbps: 1500, ActiveReaders: 2}
}
func (f *fakeRTSP) GetClients() []rtsp.ClientInfo {
	return []rtsp.ClientInfo{{ID: "sess-1", RemoteIP: "10.0.0.1", Path: "Profile_1", Transport: "TCP/RTP", Duration: 5}}
}
func (f *fakeRTSP) CloseStream(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = append(f.closed, token)
}

type testEnv struct {
	mux     *http.ServeMux
	handler *APIHandler
	cfg     *config.Manager
	sup     *fakeSupervisor
	rtsp    *fakeRTSP
	ptz     *onvif.PTZController
	logs    *logger.RingLogger
	store   *frames.Store
}

type silentSynth struct{}

func (silentSynth) Name() string         { return "silent" }
func (silentSynth) Supports(string) bool { return true }
func (silentSynth) Synthesize(context.Context, string, string) (timesignal.PCM, error) {
	return timesignal.PCM{SampleRate: timesignal.SampleRate, Samples: make([]int16, 480)}, nil
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	cfgMgr, err := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	ptz := onvif.NewPTZController(cfgMgr)
	sup := &fakeSupervisor{status: []supervisor.WorkerStatus{{Token: "Profile_1", Running: true, PID: 42}}}
	rt := &fakeRTSP{}
	logs := logger.NewRingLogger(100)
	ts := timesignal.NewServiceWith(silentSynth{}, time.Now)
	store := frames.NewStore()

	h := newAPIHandler(cfgMgr, sup, rt, ptz, store, ts, logs)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	// PTZ persists asynchronously; wait so TempDir cleanup does not race the write.
	t.Cleanup(ptz.WaitSync)
	return &testEnv{mux: mux, handler: h, cfg: cfgMgr, sup: sup, rtsp: rt, ptz: ptz, logs: logs, store: store}
}

func (e *testEnv) do(t *testing.T, method, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("invalid JSON %q: %v", rec.Body.String(), err)
	}
}

// ---- tests ---------------------------------------------------------------

func TestStatus(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, "GET", "/api/status", "")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var st StatusResponse
	decode(t, rec, &st)
	if st.Version != config.AppVersion || st.ProfilesCount != 2 || st.RTSPClients != 3 || st.BitrateKbps != 1500 {
		t.Fatalf("unexpected status: %+v", st)
	}
	if len(st.Workers) != 1 || st.Workers[0].PID != 42 {
		t.Fatalf("workers missing: %+v", st.Workers)
	}
	if e.do(t, "POST", "/api/status", "").Code != 405 {
		t.Fatal("POST should be 405")
	}
}

func TestStatusWithoutOptionalComponents(t *testing.T) {
	cfgMgr, _ := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	h := newAPIHandler(cfgMgr, nil, nil, onvif.NewPTZController(cfgMgr), nil, timesignal.NewServiceWith(silentSynth{}, time.Now), logger.NewRingLogger(10))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	for _, p := range []string{"/api/status", "/api/clients", "/api/diagnostics/export"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != 200 {
			t.Fatalf("%s without supervisor/rtsp: %d", p, rec.Code)
		}
	}
	var st StatusResponse
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/status", nil))
	decode(t, rec, &st)
	if st.Workers == nil {
		t.Fatal("workers should be an empty array, not null")
	}
}

func TestConfigGetAndPut(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, "GET", "/api/config", "")
	var cfg config.Config
	decode(t, rec, &cfg)
	if cfg.Server.HTTPPort != 8080 {
		t.Fatalf("config = %+v", cfg.Server)
	}

	cfg.Server.AuthType = "none"
	cfg.Server.LogLevel = "debug"
	body, _ := json.Marshal(map[string]any{"server": cfg.Server})
	rec = e.do(t, "PUT", "/api/config", string(body))
	if rec.Code != 200 {
		t.Fatalf("PUT status %d: %s", rec.Code, rec.Body.String())
	}
	if e.cfg.Get().Server.AuthType != "none" {
		t.Fatal("server config not applied")
	}
	if e.logs.GetMinLevel() != logger.LevelDebug {
		t.Fatal("log level from config not applied")
	}

	// Invalid values are rejected with 400 and not persisted.
	cfg.Server.HTTPPort = 0
	body, _ = json.Marshal(map[string]any{"server": cfg.Server})
	rec = e.do(t, "PUT", "/api/config", string(body))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "http_port") {
		t.Fatalf("invalid port: %d %s", rec.Code, rec.Body.String())
	}
	if e.cfg.Get().Server.HTTPPort != 8080 {
		t.Fatal("invalid config must not be persisted")
	}

	// Unknown fields and malformed JSON are rejected.
	if rec := e.do(t, "PUT", "/api/config", `{"server":{"bogus":1}}`); rec.Code != 400 {
		t.Fatalf("unknown field: %d", rec.Code)
	}
	if rec := e.do(t, "PUT", "/api/config", `{`); rec.Code != 400 {
		t.Fatalf("malformed: %d", rec.Code)
	}
	if e.do(t, "DELETE", "/api/config", "").Code != 405 {
		t.Fatal("DELETE should be 405")
	}
}

func TestProfilesCRUD(t *testing.T) {
	e := newEnv(t)

	// List
	rec := e.do(t, "GET", "/api/profiles", "")
	var list []config.ProfileConfig
	decode(t, rec, &list)
	if len(list) != 2 {
		t.Fatalf("list = %d", len(list))
	}

	// Get one / missing
	if e.do(t, "GET", "/api/profiles/Profile_2", "").Code != 200 {
		t.Fatal("GET existing")
	}
	if e.do(t, "GET", "/api/profiles/nope", "").Code != 404 {
		t.Fatal("GET missing should be 404")
	}
	if e.do(t, "GET", "/api/profiles/", "").Code != 400 {
		t.Fatal("empty token should be 400")
	}

	// Create
	newProf := `{"token":"Profile_3","name":"Custom","source_mode":"generate","video":{"codec":"H264","resolution":{"width":640,"height":360},"framerate":20,"gop_size":20,"bitrate_mode":"CBR","bitrate_limit_kbps":500},"audio":{"enabled":false}}`
	rec = e.do(t, "POST", "/api/profiles", newProf)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if e.sup.restarted[len(e.sup.restarted)-1] != "Profile_3" {
		t.Fatal("new profile worker not started")
	}
	if rec := e.do(t, "POST", "/api/profiles", newProf); rec.Code != 409 {
		t.Fatalf("duplicate should be 409, got %d", rec.Code)
	}
	if rec := e.do(t, "POST", "/api/profiles", `{"token":"bad token"}`); rec.Code != 400 {
		t.Fatalf("invalid token should be 400, got %d", rec.Code)
	}
	if rec := e.do(t, "POST", "/api/profiles", `{"token":"P4","video":{"framerate":999}}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "framerate") {
		t.Fatalf("invalid framerate: %d %s", rec.Code, rec.Body.String())
	}

	// Update: token in body is ignored, RTSP stream closed, worker restarted.
	prof, _ := e.cfg.GetProfile("Profile_1")
	prof.Token = "ignored"
	prof.Video.Framerate = 12
	body, _ := json.Marshal(prof)
	rec = e.do(t, "PUT", "/api/profiles/Profile_1", string(body))
	if rec.Code != 200 {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := e.cfg.GetProfile("Profile_1"); got.Video.Framerate != 12 || got.Token != "Profile_1" {
		t.Fatalf("update not applied: %+v", got)
	}
	if e.rtsp.closed[len(e.rtsp.closed)-1] != "Profile_1" || e.sup.restarted[len(e.sup.restarted)-1] != "Profile_1" {
		t.Fatalf("hot reload not triggered: closed=%v restarted=%v", e.rtsp.closed, e.sup.restarted)
	}
	prof.Video.Codec = "MPEG2"
	body, _ = json.Marshal(prof)
	if rec := e.do(t, "PUT", "/api/profiles/Profile_1", string(body)); rec.Code != 400 {
		t.Fatalf("invalid codec should be 400: %d", rec.Code)
	}
	if rec := e.do(t, "PUT", "/api/profiles/missing", string(body)); rec.Code == 200 {
		t.Fatal("updating a missing profile must fail")
	}

	// Delete
	rec = e.do(t, "DELETE", "/api/profiles/Profile_3", "")
	if rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if e.sup.stopped[len(e.sup.stopped)-1] != "Profile_3" || e.rtsp.closed[len(e.rtsp.closed)-1] != "Profile_3" {
		t.Fatal("delete should stop the worker and close the stream")
	}
	if e.do(t, "DELETE", "/api/profiles/Profile_3", "").Code != 404 {
		t.Fatal("deleting twice should be 404")
	}
	_ = e.do(t, "DELETE", "/api/profiles/Profile_2", "")
	if rec := e.do(t, "DELETE", "/api/profiles/Profile_1", ""); rec.Code != 400 {
		t.Fatalf("last profile must not be deletable: %d", rec.Code)
	}
	if e.do(t, "PATCH", "/api/profiles/Profile_1", "").Code != 405 {
		t.Fatal("PATCH should be 405")
	}
}

func TestConfigReset(t *testing.T) {
	e := newEnv(t)
	srv := e.cfg.Get().Server
	srv.HTTPPort = 9999
	_ = e.cfg.UpdateServerConfig(srv)

	rec := e.do(t, "POST", "/api/config/reset", "")
	if rec.Code != 200 {
		t.Fatalf("reset: %d", rec.Code)
	}
	if e.cfg.Get().Server.HTTPPort != 8080 {
		t.Fatal("reset did not restore defaults")
	}
	if len(e.sup.restarted) != 2 {
		t.Fatalf("all workers should be restarted, got %v", e.sup.restarted)
	}
	if e.do(t, "GET", "/api/config/reset", "").Code != 405 {
		t.Fatal("GET should be 405")
	}
}

func TestPTZ(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, "POST", "/api/ptz", `{"action":"absolute","pan":0.5,"tilt":-0.2,"zoom":0.3}`)
	if rec.Code != 200 {
		t.Fatalf("absolute: %d %s", rec.Code, rec.Body.String())
	}
	var st map[string]any
	decode(t, rec, &st)
	if st["pan"] != 0.5 || st["tilt"] != -0.2 || st["zoom"] != 0.3 || st["status"] != "ok" {
		t.Fatalf("ptz response = %v", st)
	}

	rec = e.do(t, "GET", "/api/ptz", "")
	decode(t, rec, &st)
	if st["pan"] != 0.5 {
		t.Fatalf("GET ptz = %v", st)
	}

	if rec := e.do(t, "POST", "/api/ptz", `{"action":"continuous","vel_pan":1}`); rec.Code != 200 {
		t.Fatalf("continuous: %d", rec.Code)
	}
	if _, _, _, moving := e.ptz.GetStatus(); !moving {
		t.Fatal("continuous move should set moving")
	}
	if rec := e.do(t, "POST", "/api/ptz", `{"action":"stop"}`); rec.Code != 200 {
		t.Fatalf("stop: %d", rec.Code)
	}
	if _, _, _, moving := e.ptz.GetStatus(); moving {
		t.Fatal("stop should clear moving")
	}
	if e.do(t, "POST", "/api/ptz", `{"action":"fly"}`).Code != 400 {
		t.Fatal("unknown action should be 400")
	}
	if e.do(t, "DELETE", "/api/ptz", "").Code != 405 {
		t.Fatal("DELETE should be 405")
	}
}

func TestPTZPresets(t *testing.T) {
	e := newEnv(t)

	rec := e.do(t, "GET", "/api/ptz/presets", "")
	if rec.Body.String() != "[]\n" {
		t.Fatalf("empty presets should serialise as [], got %q", rec.Body.String())
	}

	e.ptz.AbsoluteMove(0.4, 0.1, 0.9)
	rec = e.do(t, "POST", "/api/ptz/presets", `{"action":"save_current","name":"Lobby"}`)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	// Unnamed preset gets an automatic name.
	_ = e.do(t, "POST", "/api/ptz/presets", `{"action":"save_current"}`)
	presets := e.cfg.Get().PTZ.Presets
	if len(presets) != 2 || presets[0].Name != "Lobby" || presets[0].Zoom != 0.9 || presets[1].Name != "Preset_2" {
		t.Fatalf("presets = %+v", presets)
	}
	// Saving the same name overwrites instead of duplicating.
	e.ptz.AbsoluteMove(0, 0, 0)
	_ = e.do(t, "POST", "/api/ptz/presets", `{"action":"save_current","name":"Lobby"}`)
	presets = e.cfg.Get().PTZ.Presets
	if len(presets) != 2 || presets[0].Zoom != 0 {
		t.Fatalf("overwrite failed: %+v", presets)
	}

	e.ptz.AbsoluteMove(0.4, 0.1, 0.9)
	_ = e.do(t, "POST", "/api/ptz/presets", `{"action":"save_current","name":"Lobby"}`)
	e.ptz.AbsoluteMove(0, 0, 0)
	if rec := e.do(t, "POST", "/api/ptz/presets", `{"action":"goto","name":"Lobby"}`); rec.Code != 200 {
		t.Fatalf("goto: %d", rec.Code)
	}
	if pan, _, zoom, _ := e.ptz.GetStatus(); pan != 0.4 || zoom != 0.9 {
		t.Fatal("goto did not move the camera")
	}
	if e.do(t, "POST", "/api/ptz/presets", `{"action":"goto","name":"missing"}`).Code != 404 {
		t.Fatal("goto missing should be 404")
	}

	_ = e.do(t, "POST", "/api/ptz/presets", `{"action":"delete","name":"Lobby"}`)
	if got := e.cfg.Get().PTZ.Presets; len(got) != 1 || got[0].Name != "Preset_2" {
		t.Fatalf("delete failed: %+v", got)
	}
	if e.do(t, "POST", "/api/ptz/presets", `{"action":"x"}`).Code != 400 {
		t.Fatal("unknown action should be 400")
	}
}

func TestLogsEndpoint(t *testing.T) {
	e := newEnv(t)
	e.logs.Info("test", "hello %d", 1)
	rec := e.do(t, "GET", "/api/logs", "")
	var entries []logger.LogEntry
	decode(t, rec, &entries)
	if len(entries) != 1 || entries[0].Message != "hello 1" {
		t.Fatalf("logs = %+v", entries)
	}

	if rec := e.do(t, "PUT", "/api/logs", `{"level":"warn"}`); rec.Code != 200 {
		t.Fatalf("set level: %d", rec.Code)
	}
	if e.logs.GetMinLevel() != logger.LevelWarn {
		t.Fatal("level not applied")
	}
	if e.do(t, "PUT", "/api/logs", `{"level":"verbose"}`).Code != 400 {
		t.Fatal("unknown level should be 400")
	}
}

func TestDiagnosticsExport(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, "GET", "/api/diagnostics/export", "")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), "mockcam-diagnostics.json") {
		t.Fatalf("diag: %d %v", rec.Code, rec.Header())
	}
	var diag map[string]any
	decode(t, rec, &diag)
	for _, key := range []string{"configuration", "streaming_metrics", "workers", "active_clients", "ptz_status", "recent_logs", "version"} {
		if _, ok := diag[key]; !ok {
			t.Errorf("diagnostics missing %q", key)
		}
	}
}

func TestLicensesEndpoint(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, "GET", "/api/licenses", "")
	if rec.Code != 200 {
		t.Fatalf("licenses: %d", rec.Code)
	}
	var out struct {
		Application map[string]string `json:"application"`
		Components  []map[string]any  `json:"components"`
	}
	decode(t, rec, &out)
	if out.Application["license"] != "MIT" || out.Application["version"] != config.AppVersion {
		t.Fatalf("application = %v", out.Application)
	}
	if len(out.Components) < 10 {
		t.Fatalf("expected a full component list, got %d", len(out.Components))
	}
	joined := rec.Body.String()
	for _, want := range []string{"gortsplib", "Alpine.js", "Tailwind", "FFmpeg", "Open JTalk", "tohoku-f01"} {
		if !strings.Contains(joined, want) {
			t.Errorf("licenses missing %q", want)
		}
	}
	if e.do(t, "POST", "/api/licenses", "").Code != 405 {
		t.Fatal("POST should be 405")
	}
}

func TestSnapshot(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, "GET", "/api/snapshot/Profile_1", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("snapshot: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	img, err := jpeg.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	// 1080p profile is capped to 1280x720.
	if img.Bounds().Dx() != 1280 || img.Bounds().Dy() != 720 {
		t.Fatalf("snapshot size = %v", img.Bounds())
	}

	// Unknown profile falls back to 640x360; empty token defaults to Profile_1.
	rec = e.do(t, "GET", "/api/snapshot/unknown", "")
	img, _ = jpeg.Decode(bytes.NewReader(rec.Body.Bytes()))
	if img.Bounds().Dx() != 640 {
		t.Fatalf("fallback size = %v", img.Bounds())
	}
	if e.do(t, "GET", "/api/snapshot/", "").Code != 200 {
		t.Fatal("empty token should default to Profile_1")
	}
}

func TestMJPEGStreamsFrames(t *testing.T) {
	e := newEnv(t)
	srv := httptest.NewServer(e.mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/mjpeg/Profile_2", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "multipart/x-mixed-replace") {
		t.Fatalf("content-type = %s", resp.Header.Get("Content-Type"))
	}

	// Read the first frame: boundary, headers, JPEG body.
	br := bufio.NewReader(resp.Body)
	line, _ := br.ReadString('\n')
	if strings.TrimSpace(line) != "--frame" {
		t.Fatalf("boundary = %q", line)
	}
	var length int
	for {
		hdr, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		hdr = strings.TrimSpace(hdr)
		if hdr == "" {
			break
		}
		if strings.HasPrefix(hdr, "Content-Length: ") {
			length = atoi(strings.TrimPrefix(hdr, "Content-Length: "))
		}
	}
	frame := make([]byte, length)
	if _, err := io.ReadFull(br, frame); err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(frame)); err != nil {
		t.Fatalf("first frame is not a JPEG: %v", err)
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestWebSocketRoundTrip(t *testing.T) {
	e := newEnv(t)
	srv := httptest.NewServer(e.mux)
	defer srv.Close()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Initial PTZ state is pushed on connect.
	var msg map[string]any
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatal(err)
	}
	if msg["type"] != "ptz" {
		t.Fatalf("first message = %v", msg)
	}
	if e.handler.WSClientCount() != 1 {
		t.Fatal("client should be registered")
	}

	// A move command updates the controller and is broadcast back.
	if err := conn.WriteJSON(map[string]any{"action": "move", "pan": 0.25, "tilt": 0.5, "zoom": 0.75}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatal(err)
		}
		if msg["type"] == "ptz" && msg["pan"] == 0.25 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no PTZ broadcast received")
		}
	}
	if pan, _, zoom, _ := e.ptz.GetStatus(); pan != 0.25 || zoom != 0.75 {
		t.Fatal("move not applied")
	}

	// Log entries are broadcast too.
	e.logs.Warn("test", "broadcast me")
	for {
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatal(err)
		}
		if msg["type"] == "log" {
			entry := msg["entry"].(map[string]any)
			if entry["message"] != "broadcast me" {
				t.Fatalf("log entry = %v", entry)
			}
			break
		}
	}

	conn.Close()
	deadline = time.Now().Add(2 * time.Second)
	for e.handler.WSClientCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if e.handler.WSClientCount() != 0 {
		t.Fatal("client should be removed on disconnect")
	}
}

func TestServerHandlerServesUIAndAPI(t *testing.T) {
	cfgMgr, _ := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	authenticator := auth.NewAuthenticator(cfgMgr)
	ptz := onvif.NewPTZController(cfgMgr)
	onvifSrv := onvif.NewServer(cfgMgr, authenticator, ptz)

	srv := NewServer(cfgMgr, authenticator, nil, nil, ptz, nil, onvifSrv)
	handler, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "MockCam") {
		t.Fatalf("index: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/favicon.png", nil))
	if rec.Code != 200 {
		t.Fatalf("favicon: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/status", nil))
	if rec.Code != 200 {
		t.Fatalf("api via full handler: %d", rec.Code)
	}
	// ONVIF route is mounted (GET is rejected, meaning the handler exists).
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/onvif/device_service", nil))
	if rec.Code != 405 {
		t.Fatalf("onvif route: %d", rec.Code)
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("Close before Start should be a no-op: %v", err)
	}
}

func fakeJPEG(tag byte) []byte {
	return []byte{0xFF, 0xD8, 0xFF, 0xE0, tag, 0xFF, 0xD9}
}

func TestSnapshotPrefersLiveFrame(t *testing.T) {
	e := newEnv(t)

	// No live frame yet → synthetic JPEG, flagged as such.
	rec := e.do(t, "GET", "/api/snapshot/Profile_1", "")
	if rec.Code != 200 || rec.Header().Get(sourceHeader) != "synthetic" {
		t.Fatalf("synthetic: %d %q", rec.Code, rec.Header().Get(sourceHeader))
	}
	if _, err := jpeg.Decode(bytes.NewReader(rec.Body.Bytes())); err != nil {
		t.Fatal(err)
	}

	// Once the encoder delivered a frame it is served verbatim.
	e.store.Publish("Profile_1", fakeJPEG(7))
	rec = e.do(t, "GET", "/api/snapshot/Profile_1", "")
	if rec.Header().Get(sourceHeader) != "live" || !bytes.Equal(rec.Body.Bytes(), fakeJPEG(7)) {
		t.Fatalf("live frame not served: %q %v", rec.Header().Get(sourceHeader), rec.Body.Bytes())
	}
	if rec.Header().Get("Content-Length") != "7" || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("headers: %v", rec.Header())
	}

	// HEAD works for VMS probes and other profiles fall back independently.
	rec = e.do(t, "HEAD", "/api/snapshot/Profile_1", "")
	if rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get(sourceHeader) != "live" {
		t.Fatalf("HEAD: %d len=%d", rec.Code, rec.Body.Len())
	}
	if rec := e.do(t, "GET", "/api/snapshot/Profile_2", ""); rec.Header().Get(sourceHeader) != "synthetic" {
		t.Fatal("Profile_2 has no live frame")
	}

	// Deleting the profile forgets its frame.
	_ = e.do(t, "DELETE", "/api/profiles/Profile_1", "")
	if _, _, ok := e.store.Latest("Profile_1"); ok {
		t.Fatal("frame should be forgotten after delete")
	}
	if e.do(t, "POST", "/api/snapshot/Profile_2", "").Code != 405 {
		t.Fatal("POST should be 405")
	}
}

func TestStatusIncludesPreviewInfo(t *testing.T) {
	e := newEnv(t)
	e.store.Publish("Profile_2", fakeJPEG(1))
	var st StatusResponse
	decode(t, e.do(t, "GET", "/api/status", ""), &st)
	if len(st.Previews) != 1 || st.Previews[0].Token != "Profile_2" || st.Previews[0].Frames != 1 {
		t.Fatalf("previews = %+v", st.Previews)
	}
}

func TestMJPEGStreamsLiveFrames(t *testing.T) {
	e := newEnv(t)
	e.store.Publish("Profile_1", fakeJPEG(1))
	srv := httptest.NewServer(e.mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/mjpeg/Profile_1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get(sourceHeader) != "live" {
		t.Fatalf("source = %q", resp.Header.Get(sourceHeader))
	}

	readFrame := func(br *bufio.Reader) []byte {
		t.Helper()
		line, err := br.ReadString('\n')
		if err != nil || strings.TrimSpace(line) != "--frame" {
			t.Fatalf("boundary = %q err=%v", line, err)
		}
		length := 0
		for {
			hdr, err := br.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if hdr = strings.TrimSpace(hdr); hdr == "" {
				break
			}
			if strings.HasPrefix(hdr, "Content-Length: ") {
				length = atoi(strings.TrimPrefix(hdr, "Content-Length: "))
			}
		}
		frame := make([]byte, length)
		if _, err := io.ReadFull(br, frame); err != nil {
			t.Fatal(err)
		}
		_, _ = br.ReadString('\n') // trailing CRLF
		return frame
	}

	br := bufio.NewReader(resp.Body)
	if first := readFrame(br); !bytes.Equal(first, fakeJPEG(1)) {
		t.Fatalf("first frame = %v", first)
	}
	// A newly published frame is pushed immediately (well before the keepalive).
	e.store.Publish("Profile_1", fakeJPEG(2))
	if second := readFrame(br); !bytes.Equal(second, fakeJPEG(2)) {
		t.Fatalf("second frame = %v", second)
	}
}

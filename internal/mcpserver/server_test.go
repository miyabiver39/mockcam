package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mockcam/internal/camera"
	"mockcam/internal/config"
	"mockcam/internal/frames"
	"mockcam/internal/logger"
	"mockcam/internal/onvif"
	"mockcam/internal/supervisor"
)

type fakeSup struct{ restarted, stopped []string }

func (f *fakeSup) RestartProfile(t string) error { f.restarted = append(f.restarted, t); return nil }
func (f *fakeSup) StopProfile(t string)          { f.stopped = append(f.stopped, t) }
func (f *fakeSup) Status() []supervisor.WorkerStatus {
	return []supervisor.WorkerStatus{{Token: "Profile_1", Running: true, PID: 1}}
}

type env struct {
	session *mcp.ClientSession
	core    *camera.Controller
	sup     *fakeSup
	store   *frames.Store
}

// newEnv wires a controller with fakes to an MCP client over in-memory pipes.
func newEnv(t *testing.T) *env {
	t.Helper()
	cfg, err := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	ptz := onvif.NewPTZController(cfg)
	t.Cleanup(ptz.WaitSync)
	sup, store := &fakeSup{}, frames.NewStore()
	core := camera.New(cfg, sup, nil, ptz, store, logger.NewRingLogger(100))

	srv := New(core)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return &env{session: session, core: core, sup: sup, store: store}
}

func (e *env) call(t *testing.T, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := e.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	return res
}

func (e *env) callOK(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	res := e.call(t, name, args)
	if res.IsError {
		t.Fatalf("%s returned an error: %s", name, text(res))
	}
	out, _ := res.StructuredContent.(map[string]any)
	return out
}

func text(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestToolListAndAnnotations(t *testing.T) {
	e := newEnv(t)
	list, err := e.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*mcp.Tool{}
	for _, tl := range list.Tools {
		got[tl.Name] = tl
	}
	for _, want := range []string{
		"get_status", "get_stream_urls", "list_profiles", "get_profile", "create_profile", "update_profile", "delete_profile",
		"get_config", "update_server_config", "factory_reset", "get_ptz", "ptz_move", "list_ptz_presets",
		"save_ptz_preset", "goto_ptz_preset", "delete_ptz_preset", "get_snapshot", "list_clients", "get_logs", "set_log_level",
	} {
		tl, ok := got[want]
		if !ok {
			t.Errorf("tool %s missing", want)
			continue
		}
		if tl.Description == "" || tl.Annotations == nil {
			t.Errorf("tool %s lacks description/annotations", want)
		}
	}
	if !got["get_status"].Annotations.ReadOnlyHint || got["delete_profile"].Annotations.ReadOnlyHint {
		t.Fatal("read-only hints wrong")
	}
	if d := got["factory_reset"].Annotations.DestructiveHint; d == nil || !*d {
		t.Fatal("factory_reset must be marked destructive")
	}
}

func TestStatusProfilesAndUrls(t *testing.T) {
	e := newEnv(t)

	st := e.callOK(t, "get_status", nil)
	if st["version"] != config.AppVersion || st["profiles_count"] != float64(2) {
		t.Fatalf("status = %v", st)
	}

	profiles := e.callOK(t, "list_profiles", nil)
	if items := profiles["items"].([]any); len(items) != 2 {
		t.Fatalf("profiles = %v", profiles)
	}
	p := e.callOK(t, "get_profile", map[string]any{"token": "Profile_2"})
	if p["token"] != "Profile_2" {
		t.Fatalf("profile = %v", p)
	}
	if res := e.call(t, "get_profile", map[string]any{"token": "nope"}); !res.IsError || !strings.Contains(text(res), "not found") {
		t.Fatalf("missing profile should be a tool error: %v", text(res))
	}

	urls := e.callOK(t, "get_stream_urls", map[string]any{"host": "10.0.0.5"})
	first := urls["items"].([]any)[0].(map[string]any)
	if first["rtsp"] != "rtsp://admin:admin1234@10.0.0.5:8554/live/Profile_1" || !strings.HasSuffix(first["snapshot"].(string), "/api/snapshot/Profile_1") {
		t.Fatalf("urls = %v", first)
	}
}

func TestProfileMutationsGoThroughController(t *testing.T) {
	e := newEnv(t)

	prof := config.DefaultConfig().Profiles[1]
	prof.Token = "P3"
	e.callOK(t, "create_profile", map[string]any{"profile": prof})
	if e.sup.restarted[len(e.sup.restarted)-1] != "P3" {
		t.Fatal("create must start the worker")
	}
	if res := e.call(t, "create_profile", map[string]any{"profile": prof}); !res.IsError || !strings.Contains(text(res), "conflict") {
		t.Fatalf("duplicate: %s", text(res))
	}

	prof.Video.Framerate = 12
	e.callOK(t, "update_profile", map[string]any{"token": "P3", "profile": prof})
	if got, _ := e.core.Profile("P3"); got.Video.Framerate != 12 {
		t.Fatal("update not applied")
	}
	bad := prof
	bad.Video.Framerate = 999
	if res := e.call(t, "update_profile", map[string]any{"token": "P3", "profile": bad}); !res.IsError || !strings.Contains(text(res), "framerate") {
		t.Fatalf("validation error should surface: %s", text(res))
	}

	e.callOK(t, "delete_profile", map[string]any{"token": "P3"})
	if e.sup.stopped[len(e.sup.stopped)-1] != "P3" {
		t.Fatal("delete must stop the worker")
	}

	cfg := e.callOK(t, "get_config", nil)
	if _, ok := cfg["server"]; !ok {
		t.Fatalf("config = %v", cfg)
	}
	srv := e.core.Config().Get().Server
	srv.LogLevel = "debug"
	e.callOK(t, "update_server_config", map[string]any{"server": srv})
	if e.core.Logger().GetMinLevel() != logger.LevelDebug {
		t.Fatal("server config not applied")
	}
	reset := e.callOK(t, "factory_reset", nil)
	if reset["server"].(map[string]any)["log_level"] != nil {
		t.Fatal("factory reset should clear the log level")
	}
}

func TestPTZToolsAndPresets(t *testing.T) {
	e := newEnv(t)
	st := e.callOK(t, "ptz_move", map[string]any{"action": "absolute", "pan": 0.5, "tilt": -0.5, "zoom": 0.25})
	if st["pan"] != 0.5 || st["zoom"] != 0.25 {
		t.Fatalf("ptz = %v", st)
	}
	if res := e.call(t, "ptz_move", map[string]any{"action": "fly"}); !res.IsError {
		t.Fatal("unknown action should be a tool error")
	}
	e.callOK(t, "save_ptz_preset", map[string]any{"name": "Gate"})
	e.callOK(t, "ptz_move", map[string]any{"action": "absolute"})
	target := e.callOK(t, "goto_ptz_preset", map[string]any{"name": "Gate"})
	if target["zoom"] != 0.25 || e.callOK(t, "get_ptz", nil)["zoom"] != 0.25 {
		t.Fatalf("goto = %v", target)
	}
	presets := e.callOK(t, "list_ptz_presets", nil)
	if len(presets["presets"].([]any)) != 1 {
		t.Fatalf("presets = %v", presets)
	}
	after := e.callOK(t, "delete_ptz_preset", map[string]any{"name": "Gate"})
	if len(after["presets"].([]any)) != 0 {
		t.Fatalf("delete = %v", after)
	}
}

func TestSnapshotToolReturnsImage(t *testing.T) {
	e := newEnv(t)

	res := e.call(t, "get_snapshot", nil)
	if res.IsError {
		t.Fatal(text(res))
	}
	var img *mcp.ImageContent
	for _, c := range res.Content {
		if ic, ok := c.(*mcp.ImageContent); ok {
			img = ic
		}
	}
	if img == nil || img.MIMEType != "image/jpeg" || len(img.Data) == 0 || !strings.Contains(text(res), "synthetic") {
		t.Fatalf("expected a synthetic JPEG image, got %v / %q", img, text(res))
	}

	e.store.Publish("Profile_2", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x42, 0xFF, 0xD9})
	res = e.call(t, "get_snapshot", map[string]any{"token": "Profile_2"})
	for _, c := range res.Content {
		if ic, ok := c.(*mcp.ImageContent); ok {
			img = ic
		}
	}
	if len(img.Data) != 7 || !strings.Contains(text(res), "live") {
		t.Fatalf("live frame not returned: %d bytes, %q", len(img.Data), text(res))
	}
	if res := e.call(t, "get_snapshot", map[string]any{"token": "nope"}); !res.IsError {
		t.Fatal("unknown token should error")
	}
}

func TestLogsAndClients(t *testing.T) {
	e := newEnv(t)
	e.core.Logger().Info("t", "info line")
	e.core.Logger().Warn("t", "warn line")

	all := e.callOK(t, "get_logs", nil)
	if len(all["items"].([]any)) != 2 {
		t.Fatalf("logs = %v", all)
	}
	warn := e.callOK(t, "get_logs", map[string]any{"level": "warn", "limit": 10})
	if items := warn["items"].([]any); len(items) != 1 || items[0].(map[string]any)["message"] != "warn line" {
		t.Fatalf("filtered logs = %v", warn)
	}
	e.callOK(t, "set_log_level", map[string]any{"level": "error"})
	if e.core.Logger().GetMinLevel() != logger.LevelError {
		t.Fatal("level not applied")
	}
	if res := e.call(t, "set_log_level", map[string]any{"level": "loud"}); !res.IsError {
		t.Fatal("bad level should error")
	}
	clients := e.callOK(t, "list_clients", nil)
	if clients["items"] == nil {
		t.Fatal("clients should be an empty array without an RTSP server")
	}
}

func TestResources(t *testing.T) {
	e := newEnv(t)
	list, err := e.session.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	uris := map[string]bool{}
	for _, r := range list.Resources {
		uris[r.URI] = true
	}
	for _, want := range []string{uriConfig, uriStatus, uriLogs, uriOpenAPI, uriLicenses} {
		if !uris[want] {
			t.Errorf("resource %s missing", want)
		}
	}
	tmpl, err := e.session.ListResourceTemplates(context.Background(), nil)
	if err != nil || len(tmpl.ResourceTemplates) != 1 || tmpl.ResourceTemplates[0].URITemplate != uriSnapshot {
		t.Fatalf("templates = %v err=%v", tmpl, err)
	}

	cfgRes, err := e.session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uriConfig})
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal([]byte(cfgRes.Contents[0].Text), &cfg); err != nil || len(cfg.Profiles) != 2 {
		t.Fatalf("config resource: %v %v", err, cfg)
	}
	api, _ := e.session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uriOpenAPI})
	if !strings.HasPrefix(api.Contents[0].Text, "openapi: 3.1") {
		t.Fatal("openapi resource")
	}

	e.store.Publish("Profile_1", []byte{0xFF, 0xD8, 0xFF, 0xD9})
	snap, err := e.session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "mockcam://snapshot/Profile_1"})
	if err != nil || snap.Contents[0].MIMEType != "image/jpeg" || len(snap.Contents[0].Blob) != 4 {
		t.Fatalf("snapshot resource: %v %+v", err, snap)
	}
	if _, err := e.session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "mockcam://snapshot/nope"}); err == nil {
		t.Fatal("unknown snapshot token should fail")
	}
}

func TestStreamableHTTPHandlerServesInitialize(t *testing.T) {
	cfg, _ := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	ptz := onvif.NewPTZController(cfg)
	t.Cleanup(ptz.WaitSync)
	core := camera.New(cfg, nil, nil, ptz, nil, logger.NewRingLogger(10))
	ts := httptest.NewServer(Handler(New(core)))
	defer ts.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL, HTTPClient: ts.Client()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.InitializeResult().ServerInfo.Name != "mockcam" || !strings.Contains(session.InitializeResult().Instructions, "MockCam") {
		t.Fatalf("initialize = %+v", session.InitializeResult())
	}
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_status"})
	if err != nil || res.IsError {
		t.Fatalf("get_status over HTTP: %v %v", err, res)
	}

	// Plain GET without an MCP session is rejected, not a 200 HTML page.
	resp, _ := http.Get(ts.URL)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("bare GET should not succeed: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

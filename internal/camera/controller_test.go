package camera

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mockcam/internal/config"
	"mockcam/internal/frames"
	"mockcam/internal/logger"
	"mockcam/internal/onvif"
	"mockcam/internal/rtsp"
	"mockcam/internal/supervisor"
)

type fakeSup struct{ restarted, stopped []string }

func (f *fakeSup) RestartProfile(t string) error { f.restarted = append(f.restarted, t); return nil }
func (f *fakeSup) StopProfile(t string)          { f.stopped = append(f.stopped, t) }
func (f *fakeSup) Status() []supervisor.WorkerStatus {
	return []supervisor.WorkerStatus{{Token: "Profile_1", Running: true, PID: 7}}
}

type fakeRTSP struct{ closed []string }

func (f *fakeRTSP) GetClientCount() int64 { return 2 }
func (f *fakeRTSP) GetStats() rtsp.StreamStats {
	return rtsp.StreamStats{PacketsSent: 5, BitrateKbps: 100}
}
func (f *fakeRTSP) GetClients() []rtsp.ClientInfo { return []rtsp.ClientInfo{{ID: "s1"}} }
func (f *fakeRTSP) CloseStream(t string)          { f.closed = append(f.closed, t) }

func newController(t *testing.T) (*Controller, *fakeSup, *fakeRTSP, *frames.Store) {
	t.Helper()
	cfg, err := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	ptz := onvif.NewPTZController(cfg)
	t.Cleanup(ptz.WaitSync)
	sup, rt, store := &fakeSup{}, &fakeRTSP{}, frames.NewStore()
	c := New(cfg, sup, rt, ptz, store, logger.NewRingLogger(50))
	return c, sup, rt, store
}

func TestStatusAndDiagnostics(t *testing.T) {
	c, _, _, store := newController(t)
	fixed := time.Now().Add(90 * time.Second)
	c.SetClock(func() time.Time { return fixed })
	store.Publish("Profile_1", []byte{0xFF, 0xD8, 0xFF, 0xD9})

	st := c.Status()
	if st.Version != config.AppVersion || st.ProfilesCount != 2 || st.RTSPClients != 2 || st.UptimeSeconds < 89 {
		t.Fatalf("status = %+v", st)
	}
	if len(st.Workers) != 1 || len(st.Previews) != 1 {
		t.Fatalf("workers/previews = %+v / %+v", st.Workers, st.Previews)
	}
	diag := c.Diagnostics()
	for _, k := range []string{"configuration", "streaming_metrics", "workers", "previews", "active_clients", "ptz_status", "recent_logs"} {
		if _, ok := diag[k]; !ok {
			t.Errorf("diagnostics missing %s", k)
		}
	}
}

func TestNilOptionalComponents(t *testing.T) {
	cfg, _ := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	ptz := onvif.NewPTZController(cfg)
	t.Cleanup(ptz.WaitSync)
	c := New(cfg, nil, nil, ptz, nil, nil)

	st := c.Status()
	if st.Workers == nil || st.Previews == nil || len(c.Clients()) != 0 {
		t.Fatal("nil components must yield empty slices")
	}
	if _, src := c.Snapshot("Profile_1"); src != "synthetic" {
		t.Fatal("no frame source → synthetic")
	}
	if err := c.UpdateProfile("Profile_1", cfg.Get().Profiles[0]); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteProfile("Profile_2"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.FactoryReset(); err != nil {
		t.Fatal(err)
	}
}

func TestProfileLifecycleNotifiesComponents(t *testing.T) {
	c, sup, rt, store := newController(t)

	if err := c.CreateProfile(config.ProfileConfig{Token: " P3 "}); err != nil {
		t.Fatal(err)
	}
	if sup.restarted[len(sup.restarted)-1] != "P3" {
		t.Fatal("token should be trimmed and the worker started")
	}
	if err := c.CreateProfile(config.ProfileConfig{Token: "P3"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate → ErrConflict, got %v", err)
	}
	if err := c.CreateProfile(config.ProfileConfig{Token: "bad token"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid → ErrInvalid, got %v", err)
	}

	p, _ := c.Profile("P3")
	p.Token = "ignored"
	p.Video.Framerate = 3
	if err := c.UpdateProfile("P3", p); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Profile("P3"); got.Video.Framerate != 3 || got.Token != "P3" {
		t.Fatalf("update: %+v", got)
	}
	if rt.closed[len(rt.closed)-1] != "P3" || sup.restarted[len(sup.restarted)-1] != "P3" {
		t.Fatal("update must close the stream and restart the worker")
	}
	if err := c.UpdateProfile("nope", p); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing → ErrNotFound, got %v", err)
	}
	p.Video.Codec = "MPEG2"
	if err := c.UpdateProfile("P3", p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid codec → ErrInvalid, got %v", err)
	}

	store.Publish("P3", []byte{0xFF, 0xD8, 0xFF, 0xD9})
	if err := c.DeleteProfile("P3"); err != nil {
		t.Fatal(err)
	}
	if sup.stopped[len(sup.stopped)-1] != "P3" || rt.closed[len(rt.closed)-1] != "P3" {
		t.Fatal("delete must stop the worker and close the stream")
	}
	if _, _, ok := store.Latest("P3"); ok {
		t.Fatal("delete must forget frames")
	}
	if err := c.DeleteProfile("P3"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete → ErrNotFound, got %v", err)
	}
	_ = c.DeleteProfile("Profile_2")
	if err := c.DeleteProfile("Profile_1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("last profile → ErrInvalid, got %v", err)
	}
}

func TestServerConfigLogLevelAndReset(t *testing.T) {
	c, sup, _, _ := newController(t)

	srv := c.Config().Get().Server
	srv.LogLevel = "debug"
	if err := c.UpdateServer(srv); err != nil {
		t.Fatal(err)
	}
	if c.Logger().GetMinLevel() != logger.LevelDebug {
		t.Fatal("log level not applied")
	}
	srv.HTTPPort = 0
	if err := c.UpdateServer(srv); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid port → ErrInvalid, got %v", err)
	}
	if _, err := c.SetLogLevel("verbose"); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown level")
	}
	if lvl, err := c.SetLogLevel(" warn "); err != nil || lvl != logger.LevelWarn {
		t.Fatalf("set level: %v %v", lvl, err)
	}
	c.Logger().Error("t", "boom")
	if logs := c.Logs(10); len(logs) != 1 {
		t.Fatalf("logs = %+v", logs)
	}

	cfg, err := c.FactoryReset()
	if err != nil || cfg.Server.LogLevel != "" || len(sup.restarted) != 2 {
		t.Fatalf("reset: err=%v restarted=%v", err, sup.restarted)
	}
}

func TestPTZAndPresets(t *testing.T) {
	c, _, _, _ := newController(t)

	st, err := c.PTZMove("absolute", 0.5, -0.5, 0.25, 0, 0, 0)
	if err != nil || st.Pan != 0.5 || st.Tilt != -0.5 || st.Zoom != 0.25 || st.IsMoving {
		t.Fatalf("absolute: %+v %v", st, err)
	}
	if st, _ := c.PTZMove("continuous", 0, 0, 0, 1, 0, 0); !st.IsMoving {
		t.Fatal("continuous should move")
	}
	if st, _ := c.PTZMove("stop", 0, 0, 0, 0, 0, 0); st.IsMoving {
		t.Fatal("stop should halt")
	}
	if _, err := c.PTZMove("fly", 0, 0, 0, 0, 0, 0); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown action")
	}

	if len(c.Presets()) != 0 {
		t.Fatal("presets should start empty (non-nil)")
	}
	_, _ = c.PTZMove("absolute", 0.4, 0.1, 0.9, 0, 0, 0)
	presets, err := c.SavePreset("Lobby")
	if err != nil || len(presets) != 1 || presets[0].Zoom != 0.9 {
		t.Fatalf("save: %+v %v", presets, err)
	}
	presets, _ = c.SavePreset("")
	if len(presets) != 2 || presets[1].Name != "Preset_2" {
		t.Fatalf("auto name: %+v", presets)
	}
	_, _ = c.PTZMove("absolute", 0, 0, 0, 0, 0, 0)
	presets, _ = c.SavePreset("Lobby") // overwrite
	if len(presets) != 2 || presets[0].Zoom != 0 {
		t.Fatalf("overwrite: %+v", presets)
	}
	_, _ = c.PTZMove("absolute", 0.4, 0.1, 0.9, 0, 0, 0)
	_, _ = c.SavePreset("Lobby")
	_, _ = c.PTZMove("absolute", 0, 0, 0, 0, 0, 0)
	if p, err := c.GotoPreset("Lobby"); err != nil || p.Zoom != 0.9 || c.PTZState().Zoom != 0.9 {
		t.Fatalf("goto: %+v %v", p, err)
	}
	if _, err := c.GotoPreset("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal("goto missing")
	}
	if presets, _ := c.DeletePreset("Lobby"); len(presets) != 1 || presets[0].Name != "Preset_2" {
		t.Fatalf("delete: %+v", presets)
	}
}

func TestSnapshotAndURLs(t *testing.T) {
	c, _, _, store := newController(t)

	if _, src := c.Snapshot("Profile_1"); src != "synthetic" {
		t.Fatal("synthetic before first frame")
	}
	store.Publish("Profile_1", []byte{0xFF, 0xD8, 0x01, 0xFF, 0xD9})
	if data, src := c.Snapshot("Profile_1"); src != "live" || len(data) != 5 {
		t.Fatalf("live: %s %v", src, data)
	}

	urls := c.URLs("cam.local")
	if len(urls) != 2 {
		t.Fatalf("urls = %+v", urls)
	}
	if urls[0].RTSP != "rtsp://admin:admin1234@cam.local:8554/live/Profile_1" ||
		urls[0].Snapshot != "http://cam.local:8080/api/snapshot/Profile_1" ||
		urls[0].MJPEG != "http://cam.local:8080/api/mjpeg/Profile_1" ||
		!strings.HasSuffix(urls[0].ONVIF, "/onvif/device_service") {
		t.Fatalf("urls[0] = %+v", urls[0])
	}
	// Without auth the credentials are omitted; empty host defaults to localhost.
	srv := c.Config().Get().Server
	srv.AuthType = "none"
	_ = c.UpdateServer(srv)
	if u := c.URLs(""); u[0].RTSP != "rtsp://localhost:8554/live/Profile_1" {
		t.Fatalf("no-auth url = %s", u[0].RTSP)
	}
}

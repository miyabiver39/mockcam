package supervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mockcam/internal/config"
	"mockcam/internal/frames"
)

// TestHelperProcess is not a real test: it is re-executed as a child
// process by the fake CommandFactory below and impersonates FFmpeg.
//
//	HELPER_MODE=sleep  → block until killed (a healthy encoder)
//	HELPER_MODE=exit   → exit immediately with status 1 (a crashing encoder)
//	HELPER_MODE=frames → write three fake JPEG frames to stdout, then block
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	switch os.Getenv("HELPER_MODE") {
	case "exit":
		fmt.Fprintln(os.Stderr, "simulated encoder failure")
		os.Exit(1)
	case "frames":
		for i := 0; i < 3; i++ {
			_, _ = os.Stdout.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0, byte(i), 0xFF, 0xD9})
		}
		for {
			time.Sleep(50 * time.Millisecond)
		}
	default:
		for {
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// fakeFFmpeg records every spawn and re-executes the test binary as the
// child process.
type fakeFFmpeg struct {
	mode string

	mu    sync.Mutex
	calls [][]string
}

func (f *fakeFFmpeg) factory(ctx context.Context, name string, args ...string) *exec.Cmd {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	f.mu.Unlock()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperProcess$", "--")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "HELPER_MODE="+f.mode)
	return cmd
}

func (f *fakeFFmpeg) spawns() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeFFmpeg) spawnsFor(token string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c, " "), "/live/"+token) {
			n++
		}
	}
	return n
}

func newTestSupervisor(t *testing.T, mode string, extra ...Option) (*Supervisor, *fakeFFmpeg, *config.Manager) {
	t.Helper()
	cfgMgr, err := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	ff := &fakeFFmpeg{mode: mode}
	opts := append([]Option{
		WithCommandFactory(ff.factory),
		WithBinary("ffmpeg"),
		WithTimings(30*time.Millisecond, 30*time.Millisecond, 100*time.Millisecond, 500*time.Millisecond),
	}, extra...)
	s := NewSupervisor(cfgMgr, opts...)
	t.Cleanup(s.StopAll)
	return s, ff, cfgMgr
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestSupervisorStartsOneWorkerPerProfile(t *testing.T) {
	s, ff, cfgMgr := newTestSupervisor(t, "sleep")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}

	want := len(cfgMgr.Get().Profiles)
	waitFor(t, "workers to start", func() bool { return ff.spawns() == want })

	waitFor(t, "workers running", func() bool {
		st := s.Status()
		if len(st) != want {
			return false
		}
		for _, w := range st {
			if !w.Running || w.PID == 0 {
				return false
			}
		}
		return true
	})

	tokens := s.RunningTokens()
	if len(tokens) != 2 || tokens[0] != "Profile_1" || tokens[1] != "Profile_2" {
		t.Fatalf("running tokens = %v", tokens)
	}

	// Each spawn used the configured binary and the profile's RTSP URL.
	ff.mu.Lock()
	defer ff.mu.Unlock()
	for _, c := range ff.calls {
		if c[0] != "ffmpeg" {
			t.Fatalf("binary = %q", c[0])
		}
		joined := strings.Join(c, " ")
		if !strings.Contains(joined, "rtsp://127.0.0.1:8554/live/Profile_") {
			t.Fatalf("args missing destination: %s", joined)
		}
	}
}

func TestSupervisorRestartsCrashedWorker(t *testing.T) {
	s, ff, _ := newTestSupervisor(t, "exit")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}

	// The crashing helper exits at once; the supervisor must respawn it.
	waitFor(t, "respawn after crash", func() bool { return ff.spawnsFor("Profile_1") >= 3 })

	waitFor(t, "restart counter", func() bool {
		for _, w := range s.Status() {
			if w.Token == "Profile_1" && w.Restarts >= 2 {
				return true
			}
		}
		return false
	})
}

func TestSupervisorRestartProfileReplacesProcess(t *testing.T) {
	s, ff, cfgMgr := newTestSupervisor(t, "sleep")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "initial workers", func() bool { return ff.spawns() == 2 })

	var oldPID int
	waitFor(t, "pid", func() bool {
		for _, w := range s.Status() {
			if w.Token == "Profile_1" && w.PID != 0 {
				oldPID = w.PID
				return true
			}
		}
		return false
	})

	// Change the profile and hot-reload it.
	prof, _ := cfgMgr.GetProfile("Profile_1")
	prof.Video.Framerate = 5
	if err := cfgMgr.UpdateProfile("Profile_1", prof); err != nil {
		t.Fatal(err)
	}
	if err := s.RestartProfile("Profile_1"); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "new process", func() bool {
		for _, w := range s.Status() {
			if w.Token == "Profile_1" && w.Running && w.PID != oldPID {
				return true
			}
		}
		return false
	})
	if ff.spawnsFor("Profile_1") != 2 || ff.spawnsFor("Profile_2") != 1 {
		t.Fatalf("spawns: P1=%d P2=%d (Profile_2 must not be touched)", ff.spawnsFor("Profile_1"), ff.spawnsFor("Profile_2"))
	}

	// The new invocation carries the updated configuration.
	ff.mu.Lock()
	last := strings.Join(ff.calls[len(ff.calls)-1], " ")
	ff.mu.Unlock()
	if !strings.Contains(last, "rate=5") {
		t.Fatalf("restarted worker should use the new framerate: %s", last)
	}

	if err := s.RestartProfile("does-not-exist"); err == nil {
		t.Fatal("unknown profile should error")
	}
}

func TestSupervisorStopProfileAndStopAll(t *testing.T) {
	s, ff, _ := newTestSupervisor(t, "sleep")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "initial workers", func() bool { return ff.spawns() == 2 })

	s.StopProfile("Profile_2")
	if tokens := s.RunningTokens(); len(tokens) != 1 || tokens[0] != "Profile_1" {
		t.Fatalf("after StopProfile: %v", tokens)
	}
	s.StopProfile("Profile_2") // idempotent

	s.StopAll()
	if len(s.RunningTokens()) != 0 {
		t.Fatal("StopAll should remove every worker")
	}
	// No respawn may happen after StopAll.
	before := ff.spawns()
	time.Sleep(100 * time.Millisecond)
	if ff.spawns() != before {
		t.Fatal("workers respawned after StopAll")
	}
}

func TestSupervisorUsesConfiguredPorts(t *testing.T) {
	s, ff, cfgMgr := newTestSupervisor(t, "sleep")
	cfg := cfgMgr.Get()
	cfg.Server.RTSPPort = 9554
	cfg.Server.HTTPPort = 9080
	if err := cfgMgr.UpdateServerConfig(cfg.Server); err != nil {
		t.Fatal(err)
	}
	prof, _ := cfgMgr.GetProfile("Profile_1")
	prof.Audio.Mode = "time_signal_ja"
	_ = cfgMgr.UpdateProfile("Profile_1", prof)

	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "workers", func() bool { return ff.spawns() == 2 })

	ff.mu.Lock()
	defer ff.mu.Unlock()
	var p1 string
	for _, c := range ff.calls {
		if strings.Contains(strings.Join(c, " "), "/live/Profile_1") {
			p1 = strings.Join(c, " ")
		}
	}
	if !strings.Contains(p1, "rtsp://127.0.0.1:9554/live/Profile_1") {
		t.Fatalf("RTSP port not propagated: %s", p1)
	}
	if !strings.Contains(p1, "http://127.0.0.1:9080/api/audio/timesignal?lang=ja") {
		t.Fatalf("HTTP port not propagated to the time-signal input: %s", p1)
	}
}

func TestSupervisorDeliversPreviewFrames(t *testing.T) {
	store := frames.NewStore()
	s, ff, _ := newTestSupervisor(t, "frames", WithFrameSink(store))
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "workers", func() bool { return ff.spawns() == 2 })

	// With a sink configured the preview output is part of the command line.
	ff.mu.Lock()
	joined := strings.Join(ff.calls[0], " ")
	ff.mu.Unlock()
	if !strings.Contains(joined, "-f mjpeg pipe:1") {
		t.Fatalf("preview output missing: %s", joined)
	}

	waitFor(t, "frames from both workers", func() bool {
		f1, _, ok1 := store.Latest("Profile_1")
		f2, _, ok2 := store.Latest("Profile_2")
		return ok1 && ok2 && f1.Seq == 3 && f2.Seq == 3
	})
	frame, _, _ := store.Latest("Profile_1")
	if frame.JPEG[0] != 0xFF || frame.JPEG[1] != 0xD8 || frame.JPEG[len(frame.JPEG)-1] != 0xD9 {
		t.Fatalf("unexpected frame bytes: %v", frame.JPEG)
	}

	// Without a sink no preview output is requested.
	s2, ff2, _ := newTestSupervisor(t, "sleep")
	_ = s2.Start()
	waitFor(t, "workers", func() bool { return ff2.spawns() == 2 })
	ff2.mu.Lock()
	defer ff2.mu.Unlock()
	if strings.Contains(strings.Join(ff2.calls[0], " "), "pipe:1") {
		t.Fatal("preview output should be disabled without a sink")
	}
}

func TestHelpers(t *testing.T) {
	if tailOf("  abcdef  ", 3) != "def" {
		t.Fatal("tailOf")
	}
	if tailOf("ab", 3) != "ab" {
		t.Fatal("tailOf short")
	}
	if exitDescription(nil) != "exit 0" {
		t.Fatal("exitDescription nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepCtx(ctx, time.Second) {
		t.Fatal("cancelled context should abort the sleep")
	}
	if !sleepCtx(context.Background(), time.Millisecond) {
		t.Fatal("sleep should complete")
	}
}

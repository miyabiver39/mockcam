// Package supervisor runs one FFmpeg subprocess per stream profile, restarts
// it when it dies, and hot-reloads individual profiles on configuration
// changes without disturbing the others.
package supervisor

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"mockcam/internal/config"
)

// CommandFactory creates the process for one FFmpeg invocation. It exists so
// tests can substitute a harmless helper process for the real binary.
type CommandFactory func(ctx context.Context, name string, args ...string) *exec.Cmd

// Option customises a Supervisor.
type Option func(*Supervisor)

// WithCommandFactory overrides how worker processes are spawned.
func WithCommandFactory(f CommandFactory) Option {
	return func(s *Supervisor) { s.newCommand = f }
}

// WithBinary overrides the FFmpeg executable name/path.
func WithBinary(name string) Option {
	return func(s *Supervisor) { s.binary = name }
}

// WithTimings overrides the restart/stop delays (mainly to speed up tests).
func WithTimings(restartDelay, startFailDelay, stopGrace, killGrace time.Duration) Option {
	return func(s *Supervisor) {
		s.restartDelay = restartDelay
		s.startFailDelay = startFailDelay
		s.stopGrace = stopGrace
		s.killGrace = killGrace
	}
}

// WorkerStatus is a read-only view of one worker for diagnostics.
type WorkerStatus struct {
	Token    string `json:"token"`
	Running  bool   `json:"running"`
	Restarts int    `json:"restarts"`
	PID      int    `json:"pid,omitempty"`
}

type profileWorker struct {
	token   string
	profile config.ProfileConfig
	cancel  context.CancelFunc
	done    chan struct{}

	mu       sync.Mutex
	proc     *os.Process // set only after a successful Start, guarded by mu
	stopping bool
	restarts int
}

// Supervisor manages FFmpeg subprocesses for all configured profiles.
type Supervisor struct {
	cfgMgr     *config.Manager
	newCommand CommandFactory
	binary     string

	restartDelay   time.Duration
	startFailDelay time.Duration
	stopGrace      time.Duration
	killGrace      time.Duration

	mu      sync.Mutex
	workers map[string]*profileWorker
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewSupervisor creates a Supervisor. Without options it spawns "ffmpeg"
// from PATH with production timings.
func NewSupervisor(cfgMgr *config.Manager, opts ...Option) *Supervisor {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Supervisor{
		cfgMgr:         cfgMgr,
		newCommand:     exec.CommandContext,
		binary:         "ffmpeg",
		restartDelay:   1 * time.Second,
		startFailDelay: 2 * time.Second,
		stopGrace:      300 * time.Millisecond,
		killGrace:      500 * time.Millisecond,
		workers:        make(map[string]*profileWorker),
		ctx:            ctx,
		cancel:         cancel,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Start launches FFmpeg workers for all configured profiles.
func (s *Supervisor) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.cfgMgr.Get()
	for _, p := range cfg.Profiles {
		s.startWorkerLocked(p, cfg.Server.RTSPPort, cfg.Server.HTTPPort)
	}
	return nil
}

func (s *Supervisor) startWorkerLocked(profile config.ProfileConfig, rtspPort, httpPort int) {
	workerCtx, workerCancel := context.WithCancel(s.ctx)
	w := &profileWorker{
		token:   profile.Token,
		profile: profile,
		cancel:  workerCancel,
		done:    make(chan struct{}),
	}
	s.workers[profile.Token] = w

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(w.done)
		s.runWorkerLoop(workerCtx, w, rtspPort, httpPort)
	}()
}

// runWorkerLoop keeps one FFmpeg process alive until the worker is stopped.
func (s *Supervisor) runWorkerLoop(ctx context.Context, w *profileWorker, rtspPort, httpPort int) {
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return
		}

		w.mu.Lock()
		if w.stopping {
			w.mu.Unlock()
			return
		}
		if attempt > 0 {
			w.restarts++
		}
		w.mu.Unlock()

		// The command is built and started outside the lock: cmd.Start writes
		// cmd.Process, so the handle is only published (under mu) afterwards.
		args := BuildFFmpegArgs(w.profile, rtspPort, httpPort)
		cmd := s.newCommand(ctx, s.binary, args...)
		var stderrBuf bytes.Buffer
		cmd.Stdout = nil
		cmd.Stderr = &stderrBuf

		log.Printf("[supervisor] Starting FFmpeg for profile '%s'...", w.token)
		if err := cmd.Start(); err != nil {
			log.Printf("[supervisor] Failed to start FFmpeg for profile '%s': %v", w.token, err)
			if !sleepCtx(ctx, s.startFailDelay) {
				return
			}
			continue
		}

		w.mu.Lock()
		w.proc = cmd.Process
		stopRequested := w.stopping
		w.mu.Unlock()
		if stopRequested {
			// A stop raced with the start: terminate the fresh process ourselves.
			_ = cmd.Process.Kill()
		}

		waitErr := cmd.Wait()

		w.mu.Lock()
		stopping := w.stopping
		w.proc = nil
		w.mu.Unlock()
		if stopping || ctx.Err() != nil {
			return
		}

		log.Printf("[supervisor] FFmpeg exited for profile '%s' (%v): %s, restarting...",
			w.token, exitDescription(waitErr), tailOf(stderrBuf.String(), 500))
		if !sleepCtx(ctx, s.restartDelay) {
			return
		}
	}
}

// RestartProfile hot-reloads a single profile with its current configuration.
func (s *Supervisor) RestartProfile(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	prof, ok := s.cfgMgr.GetProfile(token)
	if !ok {
		return fmt.Errorf("profile '%s' not found", token)
	}
	cfg := s.cfgMgr.Get()

	if old, exists := s.workers[token]; exists {
		s.stopWorker(old)
		delete(s.workers, token)
	}

	s.startWorkerLocked(prof, cfg.Server.RTSPPort, cfg.Server.HTTPPort)
	log.Printf("[supervisor] Profile '%s' restarted with updated config", token)
	return nil
}

// StopProfile stops the FFmpeg worker of a (deleted) profile.
func (s *Supervisor) StopProfile(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if old, exists := s.workers[token]; exists {
		s.stopWorker(old)
		delete(s.workers, token)
		log.Printf("[supervisor] Profile '%s' stopped", token)
	}
}

// stopWorker asks the process to terminate (SIGTERM), then kills it if it
// does not exit within stopGrace, and finally waits for the worker goroutine.
func (s *Supervisor) stopWorker(w *profileWorker) {
	w.mu.Lock()
	w.stopping = true
	proc := w.proc
	w.mu.Unlock()
	w.cancel()

	if proc != nil {
		_ = proc.Signal(syscall.SIGTERM) // no-op on Windows; Kill follows
		select {
		case <-w.done:
			return
		case <-time.After(s.stopGrace):
			_ = proc.Kill()
		}
	}

	select {
	case <-w.done:
	case <-time.After(s.killGrace):
		log.Printf("[supervisor] Worker '%s' did not exit in time", w.token)
	}
}

// StopAll gracefully shuts down all running FFmpeg processes.
func (s *Supervisor) StopAll() {
	s.cancel()

	s.mu.Lock()
	for _, w := range s.workers {
		s.stopWorker(w)
	}
	s.workers = make(map[string]*profileWorker)
	s.mu.Unlock()

	s.wg.Wait()
	log.Println("[supervisor] All FFmpeg workers stopped")
}

// Status returns a snapshot of every worker, sorted by token.
func (s *Supervisor) Status() []WorkerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]WorkerStatus, 0, len(s.workers))
	for _, w := range s.workers {
		w.mu.Lock()
		st := WorkerStatus{Token: w.token, Restarts: w.restarts}
		if w.proc != nil {
			st.Running = true
			st.PID = w.proc.Pid
		}
		w.mu.Unlock()
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Token < out[j].Token })
	return out
}

// RunningTokens returns the tokens of workers that currently exist.
func (s *Supervisor) RunningTokens() []string {
	status := s.Status()
	tokens := make([]string, 0, len(status))
	for _, st := range status {
		tokens = append(tokens, st.Token)
	}
	return tokens
}

// sleepCtx waits for d and reports false when ctx was cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func exitDescription(err error) string {
	if err == nil {
		return "exit 0"
	}
	return err.Error()
}

func tailOf(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

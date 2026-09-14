package supervisor

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"mockcam/internal/config"
)

type profileWorker struct {
	token      string
	profile    config.ProfileConfig
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	stopping   bool
	workerDone chan struct{}
	mu         sync.Mutex
}

// Supervisor manages FFmpeg subprocesses for all configured profiles.
type Supervisor struct {
	cfgMgr  *config.Manager
	workers map[string]*profileWorker
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewSupervisor creates a new Supervisor instance.
func NewSupervisor(cfgMgr *config.Manager) *Supervisor {
	ctx, cancel := context.WithCancel(context.Background())
	return &Supervisor{
		cfgMgr:  cfgMgr,
		workers: make(map[string]*profileWorker),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start launches FFmpeg workers for all profiles.
func (s *Supervisor) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.cfgMgr.Get()
	for _, p := range cfg.Profiles {
		s.startWorkerLocked(p, cfg.Server.RTSPPort)
	}
	return nil
}

func (s *Supervisor) startWorkerLocked(profile config.ProfileConfig, rtspPort int) {
	workerCtx, workerCancel := context.WithCancel(s.ctx)
	w := &profileWorker{
		token:      profile.Token,
		profile:    profile,
		cancel:     workerCancel,
		workerDone: make(chan struct{}),
	}
	s.workers[profile.Token] = w

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(w.workerDone)
		s.runWorkerLoop(workerCtx, w, rtspPort)
	}()
}

func (s *Supervisor) runWorkerLoop(ctx context.Context, w *profileWorker, rtspPort int) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		w.mu.Lock()
		if w.stopping {
			w.mu.Unlock()
			return
		}

		args := BuildFFmpegArgs(w.profile, rtspPort)
		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		var stderrBuf bytes.Buffer
		cmd.Stdout = nil
		cmd.Stderr = &stderrBuf
		w.cmd = cmd
		w.mu.Unlock()

		log.Printf("[supervisor] Starting FFmpeg for profile '%s'...", w.token)
		err := cmd.Start()
		if err != nil {
			log.Printf("[supervisor] Failed to start FFmpeg for profile '%s': %v", w.token, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
				continue
			}
		}

		// Wait for process completion
		waitErr := cmd.Wait()

		w.mu.Lock()
		stopping := w.stopping
		w.cmd = nil
		w.mu.Unlock()

		if stopping {
			return
		}

		stderrMsg := strings.TrimSpace(stderrBuf.String())
		if len(stderrMsg) > 300 {
			stderrMsg = stderrMsg[len(stderrMsg)-300:]
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Second):
			if waitErr != nil && stderrMsg != "" {
				log.Printf("[supervisor] FFmpeg exited for profile '%s' (err: %v): %s, restarting...", w.token, waitErr, stderrMsg)
			} else {
				log.Printf("[supervisor] FFmpeg exited for profile '%s', restarting...", w.token)
			}
		}
	}
}

// RestartProfile hot-reloads a single profile with new configuration.
func (s *Supervisor) RestartProfile(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	prof, ok := s.cfgMgr.GetProfile(token)
	if !ok {
		return fmt.Errorf("profile '%s' not found", token)
	}
	rtspPort := s.cfgMgr.Get().Server.RTSPPort

	if oldWorker, exists := s.workers[token]; exists {
		s.stopWorker(oldWorker)
		delete(s.workers, token)
	}

	s.startWorkerLocked(prof, rtspPort)
	log.Printf("[supervisor] Profile '%s' restarted with updated config", token)
	return nil
}

// StopProfile stops an FFmpeg worker for a deleted profile.
func (s *Supervisor) StopProfile(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if oldWorker, exists := s.workers[token]; exists {
		s.stopWorker(oldWorker)
		delete(s.workers, token)
		log.Printf("[supervisor] Profile '%s' stopped", token)
	}
}

func (s *Supervisor) stopWorker(w *profileWorker) {
	w.mu.Lock()
	w.stopping = true
	cmd := w.cmd
	w.cancel()
	w.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		// Send SIGTERM where supported
		_ = cmd.Process.Signal(syscall.SIGTERM)

		// Wait briefly for clean exit, otherwise Kill immediately
		select {
		case <-w.workerDone:
		case <-time.After(300 * time.Millisecond):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			select {
			case <-w.workerDone:
			case <-time.After(500 * time.Millisecond):
			}
		}
	} else {
		select {
		case <-w.workerDone:
		case <-time.After(300 * time.Millisecond):
		}
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

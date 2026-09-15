package onvif

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"mockcam/internal/config"
)

// PTZController maintains the virtual PTZ state machine.
type PTZController struct {
	cfgMgr    *config.Manager
	pan       float64
	tilt      float64
	zoom      float64
	velPan    float64
	velTilt   float64
	velZoom   float64
	isMoving  bool
	homePan   float64
	homeTilt  float64
	homeZoom  float64
	stopChan  chan struct{}
	listeners []func(pan, tilt, zoom float64)
	mu        sync.RWMutex
	wg        sync.WaitGroup
}

// NewPTZController creates a new PTZController initialized from config.
func NewPTZController(cfgMgr *config.Manager) *PTZController {
	cfg := cfgMgr.Get()
	c := &PTZController{
		cfgMgr:   cfgMgr,
		pan:      clamp(cfg.PTZ.Pan, -1.0, 1.0),
		tilt:     clamp(cfg.PTZ.Tilt, -1.0, 1.0),
		zoom:     clamp(cfg.PTZ.Zoom, 0.0, 1.0),
		stopChan: make(chan struct{}),
	}
	return c
}

// AddListener registers a callback invoked when PTZ coordinates change.
func (c *PTZController) AddListener(fn func(pan, tilt, zoom float64)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listeners = append(c.listeners, fn)
}

func (c *PTZController) notifyListenersLocked() {
	pan, tilt, zoom := c.pan, c.tilt, c.zoom
	for _, fn := range c.listeners {
		go fn(pan, tilt, zoom)
	}
	// Persist asynchronously with waitgroup tracking
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		_ = c.cfgMgr.UpdatePTZ(pan, tilt, zoom)
	}()
}

// WaitSync waits for all pending async persistence writes to finish.
func (c *PTZController) WaitSync() {
	c.wg.Wait()
}

// GetStatus returns the current PTZ coordinates and moving state.
func (c *PTZController) GetStatus() (float64, float64, float64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pan, c.tilt, c.zoom, c.isMoving
}

// AbsoluteMove immediately positions the camera at the given coordinates.
func (c *PTZController) AbsoluteMove(pan, tilt, zoom float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stopContinuousMoveLocked()

	c.pan = clamp(pan, -1.0, 1.0)
	c.tilt = clamp(tilt, -1.0, 1.0)
	c.zoom = clamp(zoom, 0.0, 1.0)
	c.isMoving = false

	c.notifyListenersLocked()
}

// RelativeMove shifts the current position by the given translation and
// clamps the result to the coordinate space.
func (c *PTZController) RelativeMove(dPan, dTilt, dZoom float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stopContinuousMoveLocked()

	c.pan = clamp(c.pan+dPan, -1.0, 1.0)
	c.tilt = clamp(c.tilt+dTilt, -1.0, 1.0)
	c.zoom = clamp(c.zoom+dZoom, 0.0, 1.0)
	c.isMoving = false

	c.notifyListenersLocked()
}

// SetHome records the current position as the home position (kept in memory).
func (c *PTZController) SetHome() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.homePan, c.homeTilt, c.homeZoom = c.pan, c.tilt, c.zoom
}

// GotoHome moves to the home position (pan/tilt/zoom 0 until SetHome is called).
func (c *PTZController) GotoHome() {
	c.mu.RLock()
	pan, tilt, zoom := c.homePan, c.homeTilt, c.homeZoom
	c.mu.RUnlock()
	c.AbsoluteMove(pan, tilt, zoom)
}

// Presets lists the presets persisted in settings.json (never nil).
func (c *PTZController) Presets() []config.PTZPreset {
	presets := c.cfgMgr.Get().PTZ.Presets
	if presets == nil {
		presets = []config.PTZPreset{}
	}
	return presets
}

// SavePreset stores the current position under name and persists the list.
// An empty name is auto-generated; an existing preset with the same name is
// overwritten. The preset name doubles as the ONVIF preset token.
func (c *PTZController) SavePreset(name string) ([]config.PTZPreset, error) {
	presets := c.Presets()
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("Preset_%d", len(presets)+1)
	}
	pan, tilt, zoom, _ := c.GetStatus()
	p := config.PTZPreset{Name: name, Pan: pan, Tilt: tilt, Zoom: zoom}

	replaced := false
	for i := range presets {
		if presets[i].Name == name {
			presets[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		presets = append(presets, p)
	}
	if err := c.cfgMgr.UpdatePTZPresets(presets); err != nil {
		return nil, err
	}
	return presets, nil
}

// GotoPreset moves to the named preset. ok is false when it does not exist.
func (c *PTZController) GotoPreset(name string) (config.PTZPreset, bool) {
	for _, p := range c.Presets() {
		if p.Name == strings.TrimSpace(name) {
			c.AbsoluteMove(p.Pan, p.Tilt, p.Zoom)
			return p, true
		}
	}
	return config.PTZPreset{}, false
}

// DeletePreset removes the named preset (no error when it does not exist).
func (c *PTZController) DeletePreset(name string) ([]config.PTZPreset, error) {
	name = strings.TrimSpace(name)
	filtered := make([]config.PTZPreset, 0)
	for _, p := range c.Presets() {
		if p.Name != name {
			filtered = append(filtered, p)
		}
	}
	if err := c.cfgMgr.UpdatePTZPresets(filtered); err != nil {
		return nil, err
	}
	return filtered, nil
}

// ContinuousMove starts continuous movement with velocity vector.
// Updates virtual coordinates by 10% per second.
func (c *PTZController) ContinuousMove(velPan, velTilt, velZoom float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stopContinuousMoveLocked()

	c.velPan = clamp(velPan, -1.0, 1.0)
	c.velTilt = clamp(velTilt, -1.0, 1.0)
	c.velZoom = clamp(velZoom, -1.0, 1.0)
	c.isMoving = true

	c.stopChan = make(chan struct{})
	go c.runContinuousLoop(c.stopChan)
}

func (c *PTZController) runContinuousLoop(stopChan <-chan struct{}) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			c.mu.Lock()
			if !c.isMoving {
				c.mu.Unlock()
				return
			}
			// dt = 0.1s, velocity scaling 10% (0.1) per second
			delta := 0.1 * 0.1
			newPan := clamp(c.pan+c.velPan*delta, -1.0, 1.0)
			newTilt := clamp(c.tilt+c.velTilt*delta, -1.0, 1.0)
			newZoom := clamp(c.zoom+c.velZoom*delta, 0.0, 1.0)

			changed := newPan != c.pan || newTilt != c.tilt || newZoom != c.zoom
			c.pan = newPan
			c.tilt = newTilt
			c.zoom = newZoom

			if changed {
				c.notifyListenersLocked()
			}
			c.mu.Unlock()
		}
	}
}

// Stop halts any active continuous movement.
func (c *PTZController) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopContinuousMoveLocked()
	c.notifyListenersLocked()
}

func (c *PTZController) stopContinuousMoveLocked() {
	if c.isMoving {
		c.isMoving = false
		c.velPan = 0
		c.velTilt = 0
		c.velZoom = 0
		select {
		case <-c.stopChan:
		default:
			close(c.stopChan)
		}
	}
}

func clamp(val, min, max float64) float64 {
	if math.IsNaN(val) {
		return min
	}
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

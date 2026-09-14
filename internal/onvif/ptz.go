package onvif

import (
	"math"
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
	stopChan  chan struct{}
	listeners []func(pan, tilt, zoom float64)
	mu        sync.RWMutex
}

// NewPTZController creates a new PTZController initialized from config.
func NewPTZController(cfgMgr *config.Manager) *PTZController {
	cfg := cfgMgr.Get()
	c := &PTZController{
		cfgMgr:  cfgMgr,
		pan:     clamp(cfg.PTZ.Pan, -1.0, 1.0),
		tilt:    clamp(cfg.PTZ.Tilt, -1.0, 1.0),
		zoom:    clamp(cfg.PTZ.Zoom, 0.0, 1.0),
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
	// Persist asynchronously
	go func() {
		_ = c.cfgMgr.UpdatePTZ(pan, tilt, zoom)
	}()
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

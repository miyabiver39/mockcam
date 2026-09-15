// Package camera is the application core shared by every management surface
// (REST API, WebSocket, MCP). It owns the business rules — validation, which
// components to notify on a change, fallbacks — while the transports stay
// thin adapters.
package camera

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"mockcam/internal/config"
	"mockcam/internal/frames"
	"mockcam/internal/logger"
	"mockcam/internal/rtsp"
	"mockcam/internal/supervisor"
)

// Sentinel errors let transports map failures to status codes / MCP errors.
var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid")
	ErrConflict = errors.New("conflict")
)

// StreamSupervisor is the subset of supervisor.Supervisor the core needs.
type StreamSupervisor interface {
	RestartProfile(token string) error
	StopProfile(token string)
	Status() []supervisor.WorkerStatus
}

// StreamServer is the subset of rtsp.Server the core needs.
type StreamServer interface {
	GetClientCount() int64
	GetStats() rtsp.StreamStats
	GetClients() []rtsp.ClientInfo
	CloseStream(token string)
}

// PTZ is the subset of onvif.PTZController the core needs.
type PTZ interface {
	GetStatus() (pan, tilt, zoom float64, moving bool)
	AbsoluteMove(pan, tilt, zoom float64)
	ContinuousMove(velPan, velTilt, velZoom float64)
	Stop()
	AddListener(fn func(pan, tilt, zoom float64))
	Presets() []config.PTZPreset
	SavePreset(name string) ([]config.PTZPreset, error)
	GotoPreset(name string) (config.PTZPreset, bool)
	DeletePreset(name string) ([]config.PTZPreset, error)
}

// FrameSource provides the latest live JPEG frame per profile.
type FrameSource interface {
	Latest(token string) (frame frames.Frame, next <-chan struct{}, ok bool)
	Forget(token string)
	Infos() []frames.Info
}

// Status is the runtime summary exposed by GET /api/status and the MCP
// get_status tool.
type Status struct {
	Version       string                    `json:"version"`
	Model         string                    `json:"model"`
	UptimeSeconds int64                     `json:"uptime_seconds"`
	ProfilesCount int                       `json:"profiles_count"`
	RTSPClients   int64                     `json:"rtsp_clients"`
	PacketsSent   int64                     `json:"packets_sent"`
	BytesSent     int64                     `json:"bytes_sent"`
	BitrateKbps   float64                   `json:"bitrate_kbps"`
	AuthType      string                    `json:"auth_type"`
	AuthUser      string                    `json:"auth_user"`
	LogLevel      string                    `json:"log_level"`
	RTSPPort      int                       `json:"rtsp_port"`
	HTTPPort      int                       `json:"http_port"`
	ONVIFPort     int                       `json:"onvif_port"`
	Workers       []supervisor.WorkerStatus `json:"workers"`
	Previews      []frames.Info             `json:"previews"`
}

// PTZState is the current virtual position.
type PTZState struct {
	Pan      float64 `json:"pan"`
	Tilt     float64 `json:"tilt"`
	Zoom     float64 `json:"zoom"`
	IsMoving bool    `json:"is_moving"`
}

// StreamURLs lists how a client can reach one profile.
type StreamURLs struct {
	Token    string `json:"token"`
	Name     string `json:"name"`
	RTSP     string `json:"rtsp"`
	Snapshot string `json:"snapshot"`
	MJPEG    string `json:"mjpeg"`
	ONVIF    string `json:"onvif_device_service"`
}

// Controller implements the camera's management operations.
type Controller struct {
	cfg    *config.Manager
	sup    StreamSupervisor
	rtsp   StreamServer
	ptz    PTZ
	frames FrameSource
	logs   *logger.RingLogger

	startTime time.Time
	now       func() time.Time
}

// New creates a Controller. sup, rtsp and frames may be nil; the affected
// features then degrade gracefully (no workers, no stats, synthetic snapshots).
func New(cfg *config.Manager, sup StreamSupervisor, rtsp StreamServer, ptz PTZ, frames FrameSource, logs *logger.RingLogger) *Controller {
	if logs == nil {
		logs = logger.GlobalLogger
	}
	return &Controller{
		cfg:       cfg,
		sup:       sup,
		rtsp:      rtsp,
		ptz:       ptz,
		frames:    frames,
		logs:      logs,
		startTime: time.Now(),
		now:       time.Now,
	}
}

// Config returns the configuration manager (for read-only consumers).
func (c *Controller) Config() *config.Manager { return c.cfg }

// PTZController exposes the PTZ state machine (e.g. for listeners).
func (c *Controller) PTZController() PTZ { return c.ptz }

// Logger returns the log ring buffer.
func (c *Controller) Logger() *logger.RingLogger { return c.logs }

// Frames returns the live frame source (may be nil).
func (c *Controller) Frames() FrameSource { return c.frames }

// SetClock overrides the time source (tests).
func (c *Controller) SetClock(now func() time.Time) { c.now = now }

// --- status -----------------------------------------------------------------

// Status builds the runtime summary.
func (c *Controller) Status() Status {
	cfg := c.cfg.Get()

	var rtspClients int64
	var stats rtsp.StreamStats
	if c.rtsp != nil {
		rtspClients = c.rtsp.GetClientCount()
		stats = c.rtsp.GetStats()
	}
	workers := []supervisor.WorkerStatus{}
	if c.sup != nil {
		workers = c.sup.Status()
	}
	previews := []frames.Info{}
	if c.frames != nil {
		previews = c.frames.Infos()
	}

	return Status{
		Version:       cfg.Server.DeviceInfo.FirmwareVersion,
		Model:         cfg.Server.DeviceInfo.Model,
		UptimeSeconds: int64(c.now().Sub(c.startTime).Seconds()),
		ProfilesCount: len(cfg.Profiles),
		RTSPClients:   rtspClients,
		PacketsSent:   stats.PacketsSent,
		BytesSent:     stats.BytesSent,
		BitrateKbps:   stats.BitrateKbps,
		AuthType:      cfg.Server.AuthType,
		AuthUser:      cfg.Server.AuthUser,
		LogLevel:      string(c.logs.GetMinLevel()),
		RTSPPort:      cfg.Server.RTSPPort,
		HTTPPort:      cfg.Server.HTTPPort,
		ONVIFPort:     cfg.Server.ONVIFPort,
		Workers:       workers,
		Previews:      previews,
	}
}

// Clients returns connected RTSP readers.
func (c *Controller) Clients() []rtsp.ClientInfo {
	if c.rtsp == nil {
		return []rtsp.ClientInfo{}
	}
	return c.rtsp.GetClients()
}

// Diagnostics assembles the support bundle.
func (c *Controller) Diagnostics() map[string]any {
	st := c.Status()
	pan, tilt, zoom, moving := c.ptz.GetStatus()
	var stats rtsp.StreamStats
	if c.rtsp != nil {
		stats = c.rtsp.GetStats()
	}
	return map[string]any{
		"export_time":    c.now().Format(time.RFC3339),
		"version":        config.AppVersion,
		"uptime_seconds": st.UptimeSeconds,
		"configuration":  c.cfg.Get(),
		"streaming_metrics": map[string]any{
			"packets_sent":   stats.PacketsSent,
			"bytes_sent":     stats.BytesSent,
			"bitrate_kbps":   stats.BitrateKbps,
			"active_readers": stats.ActiveReaders,
		},
		"workers":        st.Workers,
		"previews":       st.Previews,
		"active_clients": c.Clients(),
		"ptz_status": map[string]any{
			"pan":       pan,
			"tilt":      tilt,
			"zoom":      zoom,
			"is_moving": moving,
		},
		"recent_logs": c.logs.GetRecentLogs(500),
	}
}

// URLs returns the client-facing URLs of every profile for the given host
// (hostname or IP the client uses to reach MockCam).
func (c *Controller) URLs(host string) []StreamURLs {
	cfg := c.cfg.Get()
	if host == "" {
		host = "localhost"
	}
	cred := ""
	if strings.ToLower(strings.TrimSpace(cfg.Server.AuthType)) != "none" && cfg.Server.AuthType != "" {
		cred = cfg.Server.AuthUser + ":" + cfg.Server.AuthPass + "@"
	}
	out := make([]StreamURLs, 0, len(cfg.Profiles))
	for _, p := range cfg.Profiles {
		out = append(out, StreamURLs{
			Token:    p.Token,
			Name:     p.Name,
			RTSP:     fmt.Sprintf("rtsp://%s%s:%d/live/%s", cred, host, cfg.Server.RTSPPort, p.Token),
			Snapshot: fmt.Sprintf("http://%s:%d/api/snapshot/%s", host, cfg.Server.HTTPPort, p.Token),
			MJPEG:    fmt.Sprintf("http://%s:%d/api/mjpeg/%s", host, cfg.Server.HTTPPort, p.Token),
			ONVIF:    fmt.Sprintf("http://%s:%d/onvif/device_service", host, cfg.Server.HTTPPort),
		})
	}
	return out
}

// --- configuration ----------------------------------------------------------

// UpdateServer validates and persists the server section and applies the
// log level immediately.
func (c *Controller) UpdateServer(srv config.ServerConfig) error {
	if err := config.ValidateServer(srv); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := c.cfg.UpdateServerConfig(srv); err != nil {
		return err
	}
	if srv.LogLevel != "" {
		c.logs.SetMinLevel(logger.LogLevel(strings.ToUpper(srv.LogLevel)))
	}
	return nil
}

// FactoryReset restores defaults and restarts every worker.
func (c *Controller) FactoryReset() (config.Config, error) {
	if err := c.cfg.ResetToDefaults(); err != nil {
		return config.Config{}, err
	}
	cfg := c.cfg.Get()
	if c.sup != nil {
		for _, p := range cfg.Profiles {
			_ = c.sup.RestartProfile(p.Token)
		}
	}
	return cfg, nil
}

// SetLogLevel changes the runtime log level.
func (c *Controller) SetLogLevel(level string) (logger.LogLevel, error) {
	lvl := logger.LogLevel(strings.ToUpper(strings.TrimSpace(level)))
	switch lvl {
	case logger.LevelDebug, logger.LevelInfo, logger.LevelWarn, logger.LevelError:
	default:
		return "", fmt.Errorf("%w: unknown log level %q", ErrInvalid, level)
	}
	c.logs.SetMinLevel(lvl)
	return lvl, nil
}

// Logs returns up to limit recent entries.
func (c *Controller) Logs(limit int) []logger.LogEntry {
	return c.logs.GetRecentLogs(limit)
}

// --- profiles ---------------------------------------------------------------

// Profiles lists every profile.
func (c *Controller) Profiles() []config.ProfileConfig {
	return c.cfg.Get().Profiles
}

// Profile returns one profile.
func (c *Controller) Profile(token string) (config.ProfileConfig, error) {
	p, ok := c.cfg.GetProfile(token)
	if !ok {
		return config.ProfileConfig{}, fmt.Errorf("%w: profile %q", ErrNotFound, token)
	}
	return p, nil
}

// CreateProfile validates, persists and starts a new profile.
func (c *Controller) CreateProfile(p config.ProfileConfig) error {
	p.Token = strings.TrimSpace(p.Token)
	if err := config.ValidateProfile(p); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := c.cfg.AddProfile(p); err != nil {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	if c.sup != nil {
		_ = c.sup.RestartProfile(p.Token)
	}
	return nil
}

// UpdateProfile validates and persists a profile, then hot-reloads only its
// worker. The token argument is authoritative over p.Token.
func (c *Controller) UpdateProfile(token string, p config.ProfileConfig) error {
	p.Token = token
	if err := config.ValidateProfile(p); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := c.cfg.UpdateProfile(token, p); err != nil {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	// Close the stream so players reconnect with the new SDP, then restart
	// only this profile's FFmpeg worker.
	if c.rtsp != nil {
		c.rtsp.CloseStream(token)
	}
	if c.sup != nil {
		if err := c.sup.RestartProfile(token); err != nil {
			log.Printf("[camera] Warning: failed to restart FFmpeg for profile '%s': %v", token, err)
		}
	}
	return nil
}

// DeleteProfile removes a profile and stops its worker/stream.
func (c *Controller) DeleteProfile(token string) error {
	if err := c.cfg.DeleteProfile(token); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("%w: %v", ErrNotFound, err)
		}
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if c.sup != nil {
		c.sup.StopProfile(token)
	}
	if c.rtsp != nil {
		c.rtsp.CloseStream(token)
	}
	if c.frames != nil {
		c.frames.Forget(token)
	}
	return nil
}

// --- PTZ --------------------------------------------------------------------

// PTZState returns the current position.
func (c *Controller) PTZState() PTZState {
	pan, tilt, zoom, moving := c.ptz.GetStatus()
	return PTZState{Pan: pan, Tilt: tilt, Zoom: zoom, IsMoving: moving}
}

// PTZMove executes a PTZ action: "absolute" (pan/tilt/zoom), "continuous"
// (velocities) or "stop".
func (c *Controller) PTZMove(action string, pan, tilt, zoom, velPan, velTilt, velZoom float64) (PTZState, error) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "absolute":
		c.ptz.AbsoluteMove(pan, tilt, zoom)
	case "continuous":
		c.ptz.ContinuousMove(velPan, velTilt, velZoom)
	case "stop":
		c.ptz.Stop()
	default:
		return PTZState{}, fmt.Errorf("%w: unknown action %q", ErrInvalid, action)
	}
	return c.PTZState(), nil
}

// Presets lists saved PTZ presets (never nil).
func (c *Controller) Presets() []config.PTZPreset {
	return c.ptz.Presets()
}

// SavePreset stores the current position under name (auto-named when empty,
// overwriting an existing preset with the same name).
func (c *Controller) SavePreset(name string) ([]config.PTZPreset, error) {
	return c.ptz.SavePreset(name)
}

// GotoPreset moves to a saved preset.
func (c *Controller) GotoPreset(name string) (config.PTZPreset, error) {
	p, ok := c.ptz.GotoPreset(name)
	if !ok {
		return config.PTZPreset{}, fmt.Errorf("%w: preset %q", ErrNotFound, name)
	}
	return p, nil
}

// DeletePreset removes a preset (no error when it does not exist).
func (c *Controller) DeletePreset(name string) ([]config.PTZPreset, error) {
	return c.ptz.DeletePreset(name)
}

// --- media ------------------------------------------------------------------

// LiveFrame returns the newest encoder frame for token, if any.
func (c *Controller) LiveFrame(token string) (frames.Frame, <-chan struct{}, bool) {
	if c.frames == nil {
		return frames.Frame{}, nil, false
	}
	return c.frames.Latest(token)
}

// Snapshot returns the best available JPEG for token: the live encoder frame
// or the synthetic fallback. source is "live" or "synthetic".
func (c *Controller) Snapshot(token string) (jpeg []byte, source string) {
	if frame, _, ok := c.LiveFrame(token); ok {
		return frame.JPEG, "live"
	}
	return c.SyntheticSnapshot(token), "synthetic"
}

// SyntheticSnapshot renders the fallback preview for token.
func (c *Controller) SyntheticSnapshot(token string) []byte {
	prof, _ := c.cfg.GetProfile(token)
	width, height := snapshotSize(prof.Video.Resolution.Width, prof.Video.Resolution.Height)
	pan, tilt, zoom, _ := c.ptz.GetStatus()
	return EncodePreviewJPEG(width, height, pan, tilt, zoom)
}

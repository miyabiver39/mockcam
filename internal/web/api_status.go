package web

import (
	"net/http"
	"strings"
	"time"

	"mockcam/internal/config"
	"mockcam/internal/licenses"
	"mockcam/internal/logger"
	"mockcam/internal/rtsp"
	"mockcam/internal/supervisor"
)

// StatusResponse is the payload of GET /api/status.
type StatusResponse struct {
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
}

func (h *APIHandler) buildStatus() StatusResponse {
	cfg := h.cfgMgr.Get()

	var rtspClients int64
	var stats rtsp.StreamStats
	if h.rtspServer != nil {
		rtspClients = h.rtspServer.GetClientCount()
		stats = h.rtspServer.GetStats()
	}
	workers := []supervisor.WorkerStatus{}
	if h.supervisor != nil {
		workers = h.supervisor.Status()
	}

	return StatusResponse{
		Version:       cfg.Server.DeviceInfo.FirmwareVersion,
		Model:         cfg.Server.DeviceInfo.Model,
		UptimeSeconds: int64(h.now().Sub(h.startTime).Seconds()),
		ProfilesCount: len(cfg.Profiles),
		RTSPClients:   rtspClients,
		PacketsSent:   stats.PacketsSent,
		BytesSent:     stats.BytesSent,
		BitrateKbps:   stats.BitrateKbps,
		AuthType:      cfg.Server.AuthType,
		AuthUser:      cfg.Server.AuthUser,
		LogLevel:      string(h.logs.GetMinLevel()),
		RTSPPort:      cfg.Server.RTSPPort,
		HTTPPort:      cfg.Server.HTTPPort,
		ONVIFPort:     cfg.Server.ONVIFPort,
		Workers:       workers,
	}
}

func (h *APIHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, h.buildStatus())
}

func (h *APIHandler) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.cfgMgr.Get())
	case http.MethodPut:
		var updated config.Config
		if err := readJSON(r, &updated); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
			return
		}
		if err := config.ValidateServer(updated.Server); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid server config: "+err.Error())
			return
		}
		if err := h.cfgMgr.UpdateServerConfig(updated.Server); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to update server config: "+err.Error())
			return
		}
		if updated.Server.LogLevel != "" {
			h.logs.SetMinLevel(logger.LogLevel(strings.ToUpper(updated.Server.LogLevel)))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
			"server": updated.Server,
		})
	default:
		methodNotAllowed(w)
	}
}

func (h *APIHandler) handleConfigReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := h.cfgMgr.ResetToDefaults(); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to reset config: "+err.Error())
		return
	}
	cfg := h.cfgMgr.Get()
	if h.supervisor != nil {
		for _, p := range cfg.Profiles {
			_ = h.supervisor.RestartProfile(p.Token)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"message": "Factory reset completed",
		"config":  cfg,
	})
}

func (h *APIHandler) handleClients(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	clients := []rtsp.ClientInfo{}
	if h.rtspServer != nil {
		clients = h.rtspServer.GetClients()
	}
	writeJSON(w, http.StatusOK, clients)
}

func (h *APIHandler) handleLogs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.logs.GetRecentLogs(200))
	case http.MethodPut:
		var req struct {
			Level string `json:"level"`
		}
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
			return
		}
		lvl := logger.LogLevel(strings.ToUpper(strings.TrimSpace(req.Level)))
		switch lvl {
		case logger.LevelDebug, logger.LevelInfo, logger.LevelWarn, logger.LevelError:
		default:
			writeError(w, http.StatusBadRequest, "Unknown log level: "+req.Level)
			return
		}
		h.logs.SetMinLevel(lvl)
		writeJSON(w, http.StatusOK, map[string]any{
			"status":    "ok",
			"log_level": string(lvl),
		})
	default:
		methodNotAllowed(w)
	}
}

func (h *APIHandler) handleDiagnosticsExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	cfg := h.cfgMgr.Get()
	var stats rtsp.StreamStats
	clients := []rtsp.ClientInfo{}
	if h.rtspServer != nil {
		stats = h.rtspServer.GetStats()
		clients = h.rtspServer.GetClients()
	}
	workers := []supervisor.WorkerStatus{}
	if h.supervisor != nil {
		workers = h.supervisor.Status()
	}
	pan, tilt, zoom, moving := h.ptz.GetStatus()

	diag := map[string]any{
		"export_time":    h.now().Format(time.RFC3339),
		"version":        config.AppVersion,
		"uptime_seconds": int64(h.now().Sub(h.startTime).Seconds()),
		"configuration":  cfg,
		"streaming_metrics": map[string]any{
			"packets_sent":   stats.PacketsSent,
			"bytes_sent":     stats.BytesSent,
			"bitrate_kbps":   stats.BitrateKbps,
			"active_readers": stats.ActiveReaders,
		},
		"workers":        workers,
		"active_clients": clients,
		"ptz_status": map[string]any{
			"pan":       pan,
			"tilt":      tilt,
			"zoom":      zoom,
			"is_moving": moving,
		},
		"recent_logs": h.logs.GetRecentLogs(500),
	}

	w.Header().Set("Content-Disposition", "attachment; filename=mockcam-diagnostics.json")
	writeJSON(w, http.StatusOK, diag)
}

// handleLicenses serves the third-party attribution list shown in the UI.
func (h *APIHandler) handleLicenses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"application": map[string]string{
			"name":    "MockCam",
			"version": config.AppVersion,
			"license": "MIT",
			"url":     "https://github.com/miyabiver39/mockcam",
		},
		"components": licenses.Components,
	})
}

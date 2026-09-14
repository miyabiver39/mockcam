package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"mockcam/internal/camera"
	"mockcam/internal/config"
	"mockcam/internal/logger"
	"mockcam/internal/timesignal"
)

// Aliases keep the transport-facing names stable while the definitions live
// in the core package shared with the MCP server.
type (
	StreamSupervisor = camera.StreamSupervisor
	StreamServer     = camera.StreamServer
	PTZ              = camera.PTZ
	FrameSource      = camera.FrameSource
	StatusResponse   = camera.Status
)

// maxBodyBytes bounds JSON request bodies (settings are tiny).
const maxBodyBytes = 1 << 20

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// APIHandler coordinates REST and WebSocket endpoints.
type APIHandler struct {
	core          *camera.Controller
	cfgMgr        *config.Manager
	ptz           PTZ
	logs          *logger.RingLogger
	timeSignalSvc *timesignal.Service

	wsMu      sync.Mutex
	wsClients map[*websocket.Conn]bool
}

// NewAPIHandler creates a new APIHandler. superv, rtspSrv and frameSrc may
// be nil (e.g. in tests); the corresponding features then degrade gracefully
// (snapshots fall back to a synthetic preview).
func NewAPIHandler(
	cfgMgr *config.Manager,
	superv StreamSupervisor,
	rtspSrv StreamServer,
	ptzCtrl PTZ,
	frameSrc FrameSource,
) *APIHandler {
	return newAPIHandler(cfgMgr, superv, rtspSrv, ptzCtrl, frameSrc, timesignal.NewService(), logger.GlobalLogger)
}

func newAPIHandler(
	cfgMgr *config.Manager,
	superv StreamSupervisor,
	rtspSrv StreamServer,
	ptzCtrl PTZ,
	frameSrc FrameSource,
	ts *timesignal.Service,
	logs *logger.RingLogger,
) *APIHandler {
	return NewAPIHandlerWith(camera.New(cfgMgr, superv, rtspSrv, ptzCtrl, frameSrc, logs), ts)
}

// NewAPIHandlerWith builds the handler around an existing Controller so the
// REST API and the MCP server share one core instance.
func NewAPIHandlerWith(core *camera.Controller, ts *timesignal.Service) *APIHandler {
	h := &APIHandler{
		core:          core,
		cfgMgr:        core.Config(),
		ptz:           core.PTZController(),
		logs:          core.Logger(),
		timeSignalSvc: ts,
		wsClients:     make(map[*websocket.Conn]bool),
	}

	// Hook PTZ position updates to the WebSocket broadcaster.
	h.ptz.AddListener(func(pan, tilt, zoom float64) {
		_, _, _, moving := h.ptz.GetStatus()
		h.broadcastPTZ(pan, tilt, zoom, moving)
	})

	// Hook log messages to the WebSocket broadcaster.
	h.logs.Subscribe(func(entry logger.LogEntry) {
		h.broadcastLog(entry)
	})

	return h
}

// Core exposes the shared controller (used by main to wire the MCP server).
func (h *APIHandler) Core() *camera.Controller { return h.core }

// RegisterRoutes attaches REST API and WebSocket routes to mux.
func (h *APIHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/status", h.handleStatus)
	mux.HandleFunc("/api/config", h.handleConfig)
	mux.HandleFunc("/api/config/reset", h.handleConfigReset)
	mux.HandleFunc("/api/profiles", h.handleProfilesRoot)
	mux.HandleFunc("/api/profiles/", h.handleProfiles)
	mux.HandleFunc("/api/snapshot/", h.handleSnapshot)
	mux.HandleFunc("/api/mjpeg/", h.handleMJPEG)
	mux.HandleFunc("/api/audio/timesignal", h.timeSignalSvc.HandleAudioStream)
	mux.HandleFunc("/api/ptz", h.handlePTZ)
	mux.HandleFunc("/api/ptz/presets", h.handlePTZPresets)
	mux.HandleFunc("/api/clients", h.handleClients)
	mux.HandleFunc("/api/logs", h.handleLogs)
	mux.HandleFunc("/api/diagnostics/export", h.handleDiagnosticsExport)
	mux.HandleFunc("/api/licenses", h.handleLicenses)
	mux.HandleFunc("/api/docs", h.handleAPIDocs)
	mux.HandleFunc("/openapi.yaml", h.handleOpenAPI)
	mux.HandleFunc("/ws", h.handleWS)
}

// --- small helpers shared by the handlers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeCoreError maps controller sentinel errors to HTTP status codes.
func writeCoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, camera.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, camera.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, camera.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// readJSON decodes a bounded JSON body into v, rejecting unknown fields so
// that typos in setting names are surfaced instead of silently ignored.
func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected trailing data")
	}
	return nil
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
}

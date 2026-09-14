package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"mockcam/internal/config"
	"mockcam/internal/logger"
	"mockcam/internal/onvif"
	"mockcam/internal/rtsp"
	"mockcam/internal/supervisor"
	"mockcam/internal/timesignal"
)

// StreamSupervisor is the subset of supervisor.Supervisor used by the API.
type StreamSupervisor interface {
	RestartProfile(token string) error
	StopProfile(token string)
	Status() []supervisor.WorkerStatus
}

// StreamServer is the subset of rtsp.Server used by the API.
type StreamServer interface {
	GetClientCount() int64
	GetStats() rtsp.StreamStats
	GetClients() []rtsp.ClientInfo
	CloseStream(token string)
}

// PTZ is the subset of onvif.PTZController used by the API.
type PTZ interface {
	GetStatus() (pan, tilt, zoom float64, moving bool)
	AbsoluteMove(pan, tilt, zoom float64)
	ContinuousMove(velPan, velTilt, velZoom float64)
	Stop()
	AddListener(fn func(pan, tilt, zoom float64))
}

var _ PTZ = (*onvif.PTZController)(nil)
var _ StreamSupervisor = (*supervisor.Supervisor)(nil)
var _ StreamServer = (*rtsp.Server)(nil)

// maxBodyBytes bounds JSON request bodies (settings are tiny).
const maxBodyBytes = 1 << 20

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// APIHandler coordinates REST and WebSocket endpoints.
type APIHandler struct {
	cfgMgr        *config.Manager
	supervisor    StreamSupervisor
	rtspServer    StreamServer
	ptz           PTZ
	timeSignalSvc *timesignal.Service
	logs          *logger.RingLogger
	startTime     time.Time
	now           func() time.Time

	wsMu      sync.Mutex
	wsClients map[*websocket.Conn]bool
}

// NewAPIHandler creates a new APIHandler. superv and rtspSrv may be nil
// (e.g. in tests); the corresponding features then degrade gracefully.
func NewAPIHandler(
	cfgMgr *config.Manager,
	superv StreamSupervisor,
	rtspSrv StreamServer,
	ptzCtrl PTZ,
) *APIHandler {
	return newAPIHandler(cfgMgr, superv, rtspSrv, ptzCtrl, timesignal.NewService(), logger.GlobalLogger)
}

func newAPIHandler(
	cfgMgr *config.Manager,
	superv StreamSupervisor,
	rtspSrv StreamServer,
	ptzCtrl PTZ,
	ts *timesignal.Service,
	logs *logger.RingLogger,
) *APIHandler {
	h := &APIHandler{
		cfgMgr:        cfgMgr,
		supervisor:    superv,
		rtspServer:    rtspSrv,
		ptz:           ptzCtrl,
		timeSignalSvc: ts,
		logs:          logs,
		startTime:     time.Now(),
		now:           time.Now,
		wsClients:     make(map[*websocket.Conn]bool),
	}

	// Hook PTZ position updates to the WebSocket broadcaster.
	ptzCtrl.AddListener(func(pan, tilt, zoom float64) {
		_, _, _, moving := ptzCtrl.GetStatus()
		h.broadcastPTZ(pan, tilt, zoom, moving)
	})

	// Hook log messages to the WebSocket broadcaster.
	logs.Subscribe(func(entry logger.LogEntry) {
		h.broadcastLog(entry)
	})

	return h
}

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

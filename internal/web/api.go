package web

import (
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"mockcam/internal/config"
	"mockcam/internal/onvif"
	"mockcam/internal/rtsp"
	"mockcam/internal/supervisor"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// APIHandler coordinates REST and WebSocket endpoints.
type APIHandler struct {
	cfgMgr     *config.Manager
	supervisor *supervisor.Supervisor
	rtspServer *rtsp.Server
	ptz        *onvif.PTZController
	startTime  time.Time

	wsClients map[*websocket.Conn]bool
	wsMu      sync.Mutex
}

// NewAPIHandler creates a new APIHandler.
func NewAPIHandler(
	cfgMgr *config.Manager,
	superv *supervisor.Supervisor,
	rtspSrv *rtsp.Server,
	ptzCtrl *onvif.PTZController,
) *APIHandler {
	h := &APIHandler{
		cfgMgr:     cfgMgr,
		supervisor: superv,
		rtspServer: rtspSrv,
		ptz:        ptzCtrl,
		startTime:  time.Now(),
		wsClients:  make(map[*websocket.Conn]bool),
	}

	// Hook PTZ position updates to WebSocket broadcaster
	ptzCtrl.AddListener(func(pan, tilt, zoom float64) {
		_, _, _, moving := ptzCtrl.GetStatus()
		h.broadcastPTZ(pan, tilt, zoom, moving)
	})

	return h
}

// RegisterRoutes attaches REST API and WebSocket routes to mux.
func (h *APIHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/status", h.handleStatus)
	mux.HandleFunc("/api/config", h.handleConfig)
	mux.HandleFunc("/api/profiles/", h.handleProfiles)
	mux.HandleFunc("/api/snapshot/", h.handleSnapshot)
	mux.HandleFunc("/api/ptz", h.handlePTZ)
	mux.HandleFunc("/ws", h.handleWS)
}

func (h *APIHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg := h.cfgMgr.Get()
	var rtspClients int64
	if h.rtspServer != nil {
		rtspClients = h.rtspServer.GetClientCount()
	}

	status := map[string]interface{}{
		"profiles_count": len(cfg.Profiles),
		"uptime_seconds": int64(time.Since(h.startTime).Seconds()),
		"rtsp_clients":   rtspClients,
		"version":        cfg.Server.DeviceInfo.FirmwareVersion,
		"model":          cfg.Server.DeviceInfo.Model,
		"auth_type":      cfg.Server.AuthType,
		"auth_user":      cfg.Server.AuthUser,
		"rtsp_port":      cfg.Server.RTSPPort,
		"http_port":      cfg.Server.HTTPPort,
		"onvif_port":     cfg.Server.ONVIFPort,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func (h *APIHandler) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(h.cfgMgr.Get())
	case http.MethodPut:
		var updated config.Config
		if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := h.cfgMgr.UpdateServerConfig(updated.Server); err != nil {
			http.Error(w, "Failed to update server config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"server": updated.Server,
		})
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (h *APIHandler) handleProfiles(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/api/profiles/")
	if token == "" {
		http.Error(w, "Profile token required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		prof, ok := h.cfgMgr.GetProfile(token)
		if !ok {
			http.Error(w, "Profile not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(prof)

	case http.MethodPut:
		var updated config.ProfileConfig
		if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		if err := h.cfgMgr.UpdateProfile(token, updated); err != nil {
			http.Error(w, "Failed to update profile: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Trigger FFmpeg supervisor hot reload for this profile
		if h.supervisor != nil {
			if err := h.supervisor.RestartProfile(token); err != nil {
				log.Printf("[api] Warning: failed to restart FFmpeg for profile '%s': %v", token, err)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"message": "Profile updated and restarted",
			"profile": updated,
		})

	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

type ptzActionRequest struct {
	Action  string  `json:"action"` // "absolute", "continuous", "stop"
	Pan     float64 `json:"pan"`
	Tilt    float64 `json:"tilt"`
	Zoom    float64 `json:"zoom"`
	VelPan  float64 `json:"vel_pan"`
	VelTilt float64 `json:"vel_tilt"`
	VelZoom float64 `json:"vel_zoom"`
}

func (h *APIHandler) handlePTZ(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		pan, tilt, zoom, moving := h.ptz.GetStatus()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"pan":       pan,
			"tilt":      tilt,
			"zoom":      zoom,
			"is_moving": moving,
		})
		return
	}

	if r.Method == http.MethodPost {
		var req ptzActionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		switch strings.ToLower(req.Action) {
		case "absolute":
			h.ptz.AbsoluteMove(req.Pan, req.Tilt, req.Zoom)
		case "continuous":
			h.ptz.ContinuousMove(req.VelPan, req.VelTilt, req.VelZoom)
		case "stop":
			h.ptz.Stop()
		default:
			http.Error(w, "Unknown action: "+req.Action, http.StatusBadRequest)
			return
		}

		pan, tilt, zoom, moving := h.ptz.GetStatus()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "ok",
			"pan":       pan,
			"tilt":      tilt,
			"zoom":      zoom,
			"is_moving": moving,
		})
		return
	}

	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}

func (h *APIHandler) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/api/snapshot/")
	if token == "" {
		token = "Profile_1"
	}

	prof, ok := h.cfgMgr.GetProfile(token)
	width := 640
	height := 360
	if ok && prof.Video.Resolution.Width > 0 && prof.Video.Resolution.Height > 0 {
		width = prof.Video.Resolution.Width
		height = prof.Video.Resolution.Height
		// Scale down large snapshot for browser responsiveness
		if width > 1280 {
			width = 1280
			height = 720
		}
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Draw dark gradient-like background
	pan, tilt, zoom, _ := h.ptz.GetStatus()
	bgR := uint8(24 + int((pan+1.0)*15))
	bgG := uint8(28 + int((tilt+1.0)*15))
	bgB := uint8(40 + int(zoom*30))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{bgR, bgG, bgB, 255}}, image.Point{}, draw.Src)

	// Draw grid / radar lines
	gridCol := color.RGBA{60, 70, 90, 255}
	centerX := width / 2
	centerY := height / 2

	for x := 0; x < width; x += 40 {
		for y := 0; y < height; y++ {
			if y%2 == 0 {
				img.Set(x, y, gridCol)
			}
		}
	}
	for y := 0; y < height; y += 40 {
		for x := 0; x < width; x++ {
			if x%2 == 0 {
				img.Set(x, y, gridCol)
			}
		}
	}

	// Draw crosshair shifted by PTZ pan & tilt
	targetX := centerX + int(pan*float64(centerX/2))
	targetY := centerY - int(tilt*float64(centerY/2))
	crossCol := color.RGBA{0, 255, 180, 255}

	for x := targetX - 25; x <= targetX+25; x++ {
		if x >= 0 && x < width && targetY >= 0 && targetY < height {
			img.Set(x, targetY, crossCol)
		}
	}
	for y := targetY - 25; y <= targetY+25; y++ {
		if y >= 0 && y < height && targetX >= 0 && targetX < width {
			img.Set(targetX, y, crossCol)
		}
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_ = jpeg.Encode(w, img, &jpeg.Options{Quality: 80})
}

// WebSocket handler for real-time PTZ and status push
func (h *APIHandler) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[api] WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	h.wsMu.Lock()
	h.wsClients[conn] = true
	h.wsMu.Unlock()

	defer func() {
		h.wsMu.Lock()
		delete(h.wsClients, conn)
		h.wsMu.Unlock()
	}()

	// Send initial PTZ state
	pan, tilt, zoom, moving := h.ptz.GetStatus()
	_ = conn.WriteJSON(map[string]interface{}{
		"type":      "ptz",
		"pan":       pan,
		"tilt":      tilt,
		"zoom":      zoom,
		"is_moving": moving,
	})

	// Reader loop to detect disconnects and handle client actions
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}
		if action, ok := msg["action"].(string); ok {
			switch strings.ToLower(action) {
			case "move":
				p, _ := msg["pan"].(float64)
				t, _ := msg["tilt"].(float64)
				z, _ := msg["zoom"].(float64)
				h.ptz.AbsoluteMove(p, t, z)
			case "stop":
				h.ptz.Stop()
			}
		}
	}
}

func (h *APIHandler) broadcastPTZ(pan, tilt, zoom float64, isMoving bool) {
	h.wsMu.Lock()
	defer h.wsMu.Unlock()

	msg := map[string]interface{}{
		"type":      "ptz",
		"pan":       pan,
		"tilt":      tilt,
		"zoom":      zoom,
		"is_moving": isMoving,
	}

	for conn := range h.wsClients {
		if err := conn.WriteJSON(msg); err != nil {
			_ = conn.Close()
			delete(h.wsClients, conn)
		}
	}
}

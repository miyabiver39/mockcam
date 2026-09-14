package web

import (
	"fmt"
	"net/http"
	"strings"

	"mockcam/internal/config"
)

type ptzActionRequest struct {
	Action  string  `json:"action"` // "absolute", "continuous", "stop"
	Pan     float64 `json:"pan"`
	Tilt    float64 `json:"tilt"`
	Zoom    float64 `json:"zoom"`
	VelPan  float64 `json:"vel_pan"`
	VelTilt float64 `json:"vel_tilt"`
	VelZoom float64 `json:"vel_zoom"`
}

func (h *APIHandler) ptzStatus(extra map[string]any) map[string]any {
	pan, tilt, zoom, moving := h.ptz.GetStatus()
	out := map[string]any{
		"pan":       pan,
		"tilt":      tilt,
		"zoom":      zoom,
		"is_moving": moving,
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func (h *APIHandler) handlePTZ(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.ptzStatus(nil))
	case http.MethodPost:
		var req ptzActionRequest
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
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
			writeError(w, http.StatusBadRequest, "Unknown action: "+req.Action)
			return
		}
		writeJSON(w, http.StatusOK, h.ptzStatus(map[string]any{"status": "ok"}))
	default:
		methodNotAllowed(w)
	}
}

type presetRequest struct {
	Action string `json:"action"` // "save_current", "goto", "delete"
	Name   string `json:"name"`
}

func (h *APIHandler) handlePTZPresets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		presets := h.cfgMgr.Get().PTZ.Presets
		if presets == nil {
			presets = []config.PTZPreset{}
		}
		writeJSON(w, http.StatusOK, presets)

	case http.MethodPost:
		var req presetRequest
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
			return
		}
		presets := h.cfgMgr.Get().PTZ.Presets
		name := strings.TrimSpace(req.Name)

		switch strings.ToLower(req.Action) {
		case "save_current":
			pan, tilt, zoom, _ := h.ptz.GetStatus()
			if name == "" {
				name = fmt.Sprintf("Preset_%d", len(presets)+1)
			}
			presets = upsertPreset(presets, config.PTZPreset{Name: name, Pan: pan, Tilt: tilt, Zoom: zoom})
			if err := h.cfgMgr.UpdatePTZPresets(presets); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "presets": presets})

		case "goto":
			for _, p := range presets {
				if p.Name == name {
					h.ptz.AbsoluteMove(p.Pan, p.Tilt, p.Zoom)
					writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "target": p})
					return
				}
			}
			writeError(w, http.StatusNotFound, "Preset not found")

		case "delete":
			filtered := make([]config.PTZPreset, 0, len(presets))
			for _, p := range presets {
				if p.Name != name {
					filtered = append(filtered, p)
				}
			}
			if err := h.cfgMgr.UpdatePTZPresets(filtered); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "presets": filtered})

		default:
			writeError(w, http.StatusBadRequest, "Unknown action: "+req.Action)
		}

	default:
		methodNotAllowed(w)
	}
}

// upsertPreset replaces a preset with the same name or appends a new one.
func upsertPreset(presets []config.PTZPreset, p config.PTZPreset) []config.PTZPreset {
	for i := range presets {
		if presets[i].Name == p.Name {
			presets[i] = p
			return presets
		}
	}
	return append(presets, p)
}

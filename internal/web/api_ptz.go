package web

import (
	"net/http"
	"strings"
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

func (h *APIHandler) handlePTZ(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.core.PTZState())
	case http.MethodPost:
		var req ptzActionRequest
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
			return
		}
		state, err := h.core.PTZMove(req.Action, req.Pan, req.Tilt, req.Zoom, req.VelPan, req.VelTilt, req.VelZoom)
		if err != nil {
			writeCoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":    "ok",
			"pan":       state.Pan,
			"tilt":      state.Tilt,
			"zoom":      state.Zoom,
			"is_moving": state.IsMoving,
		})
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
		writeJSON(w, http.StatusOK, h.core.Presets())

	case http.MethodPost:
		var req presetRequest
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
			return
		}
		switch strings.ToLower(req.Action) {
		case "save_current":
			presets, err := h.core.SavePreset(req.Name)
			if err != nil {
				writeCoreError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "presets": presets})
		case "goto":
			target, err := h.core.GotoPreset(req.Name)
			if err != nil {
				writeCoreError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "target": target})
		case "delete":
			presets, err := h.core.DeletePreset(req.Name)
			if err != nil {
				writeCoreError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "presets": presets})
		default:
			writeError(w, http.StatusBadRequest, "Unknown action: "+req.Action)
		}

	default:
		methodNotAllowed(w)
	}
}

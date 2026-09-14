package web

import (
	"log"
	"net/http"
	"strings"

	"mockcam/internal/config"
)

// handleProfilesRoot serves GET (list) and POST (create) on /api/profiles.
func (h *APIHandler) handleProfilesRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.cfgMgr.Get().Profiles)
	case http.MethodPost:
		var newProf config.ProfileConfig
		if err := readJSON(r, &newProf); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
			return
		}
		newProf.Token = strings.TrimSpace(newProf.Token)
		if err := config.ValidateProfile(newProf); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid profile: "+err.Error())
			return
		}
		if err := h.cfgMgr.AddProfile(newProf); err != nil {
			writeError(w, http.StatusConflict, "Failed to add profile: "+err.Error())
			return
		}
		if h.supervisor != nil {
			_ = h.supervisor.RestartProfile(newProf.Token)
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"status":  "ok",
			"message": "Profile created",
			"profile": newProf,
		})
	default:
		methodNotAllowed(w)
	}
}

// handleProfiles serves GET / PUT / DELETE on /api/profiles/{token}.
func (h *APIHandler) handleProfiles(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/api/profiles/")
	if token == "" {
		writeError(w, http.StatusBadRequest, "Profile token required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		prof, ok := h.cfgMgr.GetProfile(token)
		if !ok {
			writeError(w, http.StatusNotFound, "Profile not found")
			return
		}
		writeJSON(w, http.StatusOK, prof)

	case http.MethodPut:
		var updated config.ProfileConfig
		if err := readJSON(r, &updated); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
			return
		}
		updated.Token = token // the URL is authoritative
		if err := config.ValidateProfile(updated); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid profile: "+err.Error())
			return
		}
		if err := h.cfgMgr.UpdateProfile(token, updated); err != nil {
			writeError(w, http.StatusNotFound, "Failed to update profile: "+err.Error())
			return
		}

		// Close the stream so that players reconnect and receive the new SDP,
		// then hot-reload only this profile's FFmpeg worker.
		if h.rtspServer != nil {
			h.rtspServer.CloseStream(token)
		}
		if h.supervisor != nil {
			if err := h.supervisor.RestartProfile(token); err != nil {
				log.Printf("[api] Warning: failed to restart FFmpeg for profile '%s': %v", token, err)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"message": "Profile updated and restarted",
			"profile": updated,
		})

	case http.MethodDelete:
		if err := h.cfgMgr.DeleteProfile(token); err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			writeError(w, status, "Failed to delete profile: "+err.Error())
			return
		}
		if h.supervisor != nil {
			h.supervisor.StopProfile(token)
		}
		if h.rtspServer != nil {
			h.rtspServer.CloseStream(token)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"message": "Profile deleted",
			"token":   token,
		})

	default:
		methodNotAllowed(w)
	}
}

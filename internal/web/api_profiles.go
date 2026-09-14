package web

import (
	"net/http"
	"strings"

	"mockcam/internal/config"
)

// handleProfilesRoot serves GET (list) and POST (create) on /api/profiles.
func (h *APIHandler) handleProfilesRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.core.Profiles())
	case http.MethodPost:
		var newProf config.ProfileConfig
		if err := readJSON(r, &newProf); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
			return
		}
		if err := h.core.CreateProfile(newProf); err != nil {
			writeCoreError(w, err)
			return
		}
		newProf.Token = strings.TrimSpace(newProf.Token)
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
		prof, err := h.core.Profile(token)
		if err != nil {
			writeCoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, prof)

	case http.MethodPut:
		var updated config.ProfileConfig
		if err := readJSON(r, &updated); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
			return
		}
		if err := h.core.UpdateProfile(token, updated); err != nil {
			writeCoreError(w, err)
			return
		}
		updated.Token = token
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"message": "Profile updated and restarted",
			"profile": updated,
		})

	case http.MethodDelete:
		if err := h.core.DeleteProfile(token); err != nil {
			writeCoreError(w, err)
			return
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

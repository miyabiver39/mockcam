package web

import (
	"net/http"

	"mockcam/internal/config"
	"mockcam/internal/licenses"
)

func (h *APIHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, h.core.Status())
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
		if err := h.core.UpdateServer(updated.Server); err != nil {
			writeCoreError(w, err)
			return
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
	cfg, err := h.core.FactoryReset()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to reset config: "+err.Error())
		return
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
	writeJSON(w, http.StatusOK, h.core.Clients())
}

func (h *APIHandler) handleLogs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.core.Logs(200))
	case http.MethodPut:
		var req struct {
			Level string `json:"level"`
		}
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
			return
		}
		lvl, err := h.core.SetLogLevel(req.Level)
		if err != nil {
			writeCoreError(w, err)
			return
		}
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
	w.Header().Set("Content-Disposition", "attachment; filename=mockcam-diagnostics.json")
	writeJSON(w, http.StatusOK, h.core.Diagnostics())
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

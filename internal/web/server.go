package web

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"

	"mockcam/internal/auth"
	"mockcam/internal/config"
	"mockcam/internal/onvif"
	"mockcam/internal/rtsp"
	"mockcam/internal/supervisor"
)

//go:embed static/*
var staticFiles embed.FS

// Server is the HTTP web dashboard and API server.
type Server struct {
	cfgMgr     *config.Manager
	auth       *auth.Authenticator
	apiHandler *APIHandler
	onvifSrv   *onvif.Server
	httpServer *http.Server
}

// NewServer creates a new Web server.
func NewServer(
	cfgMgr *config.Manager,
	authenticator *auth.Authenticator,
	superv *supervisor.Supervisor,
	rtspSrv *rtsp.Server,
	ptzCtrl *onvif.PTZController,
	onvifSrv *onvif.Server,
) *Server {
	apiHandler := NewAPIHandler(cfgMgr, superv, rtspSrv, ptzCtrl)
	return &Server{
		cfgMgr:     cfgMgr,
		auth:       authenticator,
		apiHandler: apiHandler,
		onvifSrv:   onvifSrv,
	}
}

// Start launches the HTTP server for Web UI, REST API, and ONVIF SOAP services.
func (s *Server) Start() error {
	cfg := s.cfgMgr.Get()
	port := cfg.Server.HTTPPort
	if port <= 0 {
		port = 8080
	}

	mux := http.NewServeMux()

	// Register ONVIF SOAP routes
	if s.onvifSrv != nil {
		s.onvifSrv.RegisterRoutes(mux)
	}

	// Register REST API & WebSocket routes
	s.apiHandler.RegisterRoutes(mux)

	// Static UI file system
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("failed to load embedded static filesystem: %w", err)
	}
	fileServer := http.FileServer(http.FS(staticFS))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Serve embedded files
		fileServer.ServeHTTP(w, r)
	})

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	log.Printf("[web] Starting Web UI & API server on :%d...", port)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[web] HTTP server error: %v", err)
		}
	}()

	return nil
}

// Close stops the HTTP server.
func (s *Server) Close() error {
	if s.httpServer != nil {
		return s.httpServer.Close()
	}
	return nil
}

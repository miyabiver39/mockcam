// Package web serves the embedded dashboard, the REST/WebSocket API and
// hosts the ONVIF SOAP routes on the same HTTP port.
package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"mockcam/internal/auth"
	"mockcam/internal/config"
	"mockcam/internal/onvif"
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

// NewServer creates a new Web server. superv and rtspSrv may be nil.
func NewServer(
	cfgMgr *config.Manager,
	authenticator *auth.Authenticator,
	superv StreamSupervisor,
	rtspSrv StreamServer,
	ptzCtrl PTZ,
	onvifSrv *onvif.Server,
) *Server {
	return &Server{
		cfgMgr:     cfgMgr,
		auth:       authenticator,
		apiHandler: NewAPIHandler(cfgMgr, superv, rtspSrv, ptzCtrl),
		onvifSrv:   onvifSrv,
	}
}

// Handler builds the complete HTTP mux (ONVIF SOAP, REST API, WebSocket,
// embedded static UI). It is exposed so tests can drive the full router
// without opening a network port.
func (s *Server) Handler() (http.Handler, error) {
	mux := http.NewServeMux()

	if s.onvifSrv != nil {
		s.onvifSrv.RegisterRoutes(mux)
	}
	s.apiHandler.RegisterRoutes(mux)

	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, fmt.Errorf("failed to load embedded static filesystem: %w", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticFS)))
	return mux, nil
}

// Start launches the HTTP server for Web UI, REST API, and ONVIF SOAP services.
func (s *Server) Start() error {
	cfg := s.cfgMgr.Get()
	port := cfg.Server.HTTPPort
	if port <= 0 {
		port = 8080
	}

	handler, err := s.Handler()
	if err != nil {
		return err
	}

	s.httpServer = &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("[web] Starting Web UI & API server on :%d...", port)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[web] HTTP server error: %v", err)
		}
	}()
	return nil
}

// Close stops the HTTP server, allowing in-flight requests a short grace period.
func (s *Server) Close() error {
	if s.httpServer == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return s.httpServer.Close()
	}
	return nil
}

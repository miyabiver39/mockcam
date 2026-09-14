package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"mockcam/internal/auth"
	"mockcam/internal/config"
	"mockcam/internal/logger"
	"mockcam/internal/onvif"
	"mockcam/internal/rtsp"
	"mockcam/internal/supervisor"
	"mockcam/internal/web"
)

func main() {
	configPathFlag := flag.String("config", "", "Path to settings.json (default: /config/settings.json or $CONFIG_PATH)")
	flag.Parse()

	configPath := *configPathFlag
	if configPath == "" {
		configPath = config.GetDefaultPath()
	}

	logger.Infof("mockcam", "Initializing MockCam v%s virtual network camera...", config.AppVersion)
	logger.Infof("mockcam", "Using config file: %s", configPath)

	// 1. Load configuration
	cfgMgr, err := config.NewManager(configPath)
	if err != nil {
		log.Fatalf("[mockcam] Failed to load configuration: %v", err)
	}
	cfg := cfgMgr.Get()
	if err := config.Validate(cfg); err != nil {
		log.Printf("[mockcam] Warning: configuration has problems (fix them in the Web UI): %v", err)
	}

	// Configure initial log level
	if cfg.Server.LogLevel != "" {
		logger.GlobalLogger.SetMinLevel(logger.LogLevel(strings.ToUpper(cfg.Server.LogLevel)))
	}

	logger.Infof("mockcam", "Configuration loaded: %d profiles configured, model=%s",
		len(cfg.Profiles), cfg.Server.DeviceInfo.Model)

	// 2. Initialize Authentication & PTZ
	authenticator := auth.NewAuthenticator(cfgMgr)
	ptzController := onvif.NewPTZController(cfgMgr)

	// 3. Initialize RTSP Server
	rtspServer := rtsp.NewServer(cfgMgr, authenticator)
	if err := rtspServer.Start(); err != nil {
		log.Fatalf("[mockcam] Failed to start RTSP server: %v", err)
	}

	// 4. Initialize Supervisor (FFmpeg workers)
	superv := supervisor.NewSupervisor(cfgMgr)
	if err := superv.Start(); err != nil {
		log.Fatalf("[mockcam] Failed to start FFmpeg supervisor: %v", err)
	}

	// 5. Initialize ONVIF SOAP & WS-Discovery
	onvifServer := onvif.NewServer(cfgMgr, authenticator, ptzController)
	discoveryServer := onvif.NewDiscoveryServer(cfgMgr)
	if err := discoveryServer.Start(); err != nil {
		log.Printf("[mockcam] Warning: WS-Discovery multicast start failed: %v", err)
	}

	// 6. Initialize Web Server (UI, API, WebSocket, and ONVIF SOAP routes)
	webServer := web.NewServer(cfgMgr, authenticator, superv, rtspServer, ptzController, onvifServer)
	if err := webServer.Start(); err != nil {
		log.Fatalf("[mockcam] Failed to start Web server: %v", err)
	}

	log.Printf("[mockcam] MockCam running successfully.")
	log.Printf("[mockcam]   - Web Dashboard: http://localhost:%d", cfg.Server.HTTPPort)
	log.Printf("[mockcam]   - RTSP Streaming: rtsp://localhost:%d/live/{token}", cfg.Server.RTSPPort)
	log.Printf("[mockcam]   - ONVIF Discovery: UDP 239.255.255.250:%d", cfg.Server.ONVIFPort)

	// 7. Wait for OS interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	sig := <-sigChan
	log.Printf("[mockcam] Received signal %v, shutting down gracefully...", sig)

	// Graceful teardown
	discoveryServer.Close()
	_ = webServer.Close()
	superv.StopAll()
	rtspServer.Close()
	_ = cfgMgr.Save()

	log.Println("[mockcam] MockCam stopped.")
}

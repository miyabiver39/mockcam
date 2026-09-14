// Command mockcam runs the virtual network camera: RTSP fan-out, ONVIF
// Profile S services, the web dashboard / REST API and the MCP server.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"mockcam/internal/auth"
	"mockcam/internal/camera"
	"mockcam/internal/config"
	"mockcam/internal/frames"
	"mockcam/internal/logger"
	"mockcam/internal/mcpserver"
	"mockcam/internal/onvif"
	"mockcam/internal/rtsp"
	"mockcam/internal/supervisor"
	"mockcam/internal/timesignal"
	"mockcam/internal/web"
)

func main() {
	configPathFlag := flag.String("config", "", "Path to settings.json (default: /config/settings.json or $CONFIG_PATH)")
	mcpStdio := flag.Bool("mcp-stdio", false, "Serve the Model Context Protocol on stdin/stdout (logs go to stderr); the camera keeps running until the MCP client disconnects")
	flag.Parse()

	if *mcpStdio {
		// stdout belongs to the MCP transport; keep every log line off it.
		log.SetOutput(os.Stderr)
	}

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

	// 4. Initialize Supervisor (FFmpeg workers) with the JPEG preview sink
	frameStore := frames.NewStore()
	superv := supervisor.NewSupervisor(cfgMgr, supervisor.WithFrameSink(frameStore))
	if err := superv.Start(); err != nil {
		log.Fatalf("[mockcam] Failed to start FFmpeg supervisor: %v", err)
	}

	// 5. Initialize ONVIF SOAP & WS-Discovery
	onvifServer := onvif.NewServer(cfgMgr, authenticator, ptzController)
	discoveryServer := onvif.NewDiscoveryServer(cfgMgr)
	if err := discoveryServer.Start(); err != nil {
		log.Printf("[mockcam] Warning: WS-Discovery multicast start failed: %v", err)
	}

	// 6. Application core shared by the REST API, WebSocket and MCP
	core := camera.New(cfgMgr, superv, rtspServer, ptzController, frameStore, logger.GlobalLogger)
	mcpSrv := mcpserver.New(core)

	// 7. Web Server (UI, API, WebSocket, ONVIF SOAP routes, MCP over HTTP)
	webServer := web.NewServerWith(core, authenticator, onvifServer, timesignal.NewService())
	webServer.Mount("/mcp", mcpserver.Handler(mcpSrv))
	if err := webServer.Start(); err != nil {
		log.Fatalf("[mockcam] Failed to start Web server: %v", err)
	}

	log.Printf("[mockcam] MockCam running successfully.")
	log.Printf("[mockcam]   - Web Dashboard: http://localhost:%d", cfg.Server.HTTPPort)
	log.Printf("[mockcam]   - API Reference: http://localhost:%d/api/docs", cfg.Server.HTTPPort)
	log.Printf("[mockcam]   - MCP endpoint:  http://localhost:%d/mcp", cfg.Server.HTTPPort)
	log.Printf("[mockcam]   - RTSP Streaming: rtsp://localhost:%d/live/{token}", cfg.Server.RTSPPort)
	log.Printf("[mockcam]   - ONVIF Discovery: UDP 239.255.255.250:%d", cfg.Server.ONVIFPort)

	// 8. Wait for an OS signal, or — in stdio mode — for the MCP client to go away.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *mcpStdio {
		log.Printf("[mockcam] Serving MCP on stdio; shutting down when the client disconnects")
		if err := mcpserver.RunStdio(ctx, mcpSrv); err != nil && ctx.Err() == nil {
			log.Printf("[mockcam] MCP stdio session ended: %v", err)
		}
	} else {
		<-ctx.Done()
	}
	log.Printf("[mockcam] Shutting down gracefully...")

	// Graceful teardown
	discoveryServer.Close()
	_ = webServer.Close()
	superv.StopAll()
	rtspServer.Close()
	_ = cfgMgr.Save()

	log.Println("[mockcam] MockCam stopped.")
}

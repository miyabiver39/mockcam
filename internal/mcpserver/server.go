// Package mcpserver exposes MockCam to AI agents through the Model Context
// Protocol. Every tool is a thin wrapper over camera.Controller, so the MCP
// surface and the REST API always behave identically.
//
// Transports:
//   - Streamable HTTP at /mcp on the management port (see Handler)
//   - stdio when the binary is started with -mcp-stdio (see RunStdio)
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mockcam/docs"
	"mockcam/internal/camera"
	"mockcam/internal/config"
	"mockcam/internal/licenses"
	"mockcam/internal/logger"
)

// Instructions is sent to clients on initialize so an agent knows how the
// pieces fit together before calling any tool.
const Instructions = `MockCam is a virtual ONVIF/RTSP network camera emulator used for VMS/NVR development and load testing.
Each stream *profile* is one FFmpeg worker publishing rtsp://<host>:<rtsp_port>/live/<token>.
Use get_status for health, list_profiles / update_profile to change video/audio settings (changes hot-reload only that profile),
ptz_move for pan/tilt/zoom, get_snapshot to see what the camera is currently sending, and get_stream_urls to hand playback URLs to a client.
Destructive tools (delete_profile, factory_reset) take effect immediately and persist to settings.json.`

// New builds the MCP server around the shared controller.
func New(core *camera.Controller) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:       "mockcam",
		Title:      "MockCam virtual network camera",
		Version:    config.AppVersion,
		WebsiteURL: "https://github.com/miyabiver39/mockcam",
	}, &mcp.ServerOptions{Instructions: Instructions})

	registerTools(srv, core)
	registerResources(srv, core)
	return srv
}

// Handler returns the Streamable HTTP transport handler for mounting at /mcp.
func Handler(srv *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		// Requests reach the container through a bridge address, and the
		// dashboard already accepts any Host, so keep MCP consistent with it.
		DisableLocalhostProtection: true,
	})
}

// RunStdio serves the MCP protocol over stdin/stdout until the client
// disconnects or ctx is cancelled.
func RunStdio(ctx context.Context, srv *mcp.Server) error {
	return srv.Run(ctx, &mcp.StdioTransport{})
}

// ---------------------------------------------------------------------------
// tools
// ---------------------------------------------------------------------------

func boolPtr(b bool) *bool { return &b }

var (
	readOnly    = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)}
	mutating    = &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)}
	idempotent  = &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)}
	destructive = &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false)}
)

type empty struct{}

type tokenInput struct {
	Token string `json:"token" jsonschema:"Profile token, e.g. Profile_1"`
}

type profileInput struct {
	Profile config.ProfileConfig `json:"profile" jsonschema:"Full profile definition (same schema as GET /api/profiles/{token})"`
}

type updateProfileInput struct {
	Token   string               `json:"token" jsonschema:"Token of the profile to update"`
	Profile config.ProfileConfig `json:"profile" jsonschema:"Full replacement profile; the token field inside is ignored"`
}

type serverInput struct {
	Server config.ServerConfig `json:"server" jsonschema:"Ports, authentication, log level and ONVIF device information"`
}

type ptzInput struct {
	Action  string  `json:"action" jsonschema:"absolute, continuous or stop"`
	Pan     float64 `json:"pan,omitempty" jsonschema:"-1..1 (absolute)"`
	Tilt    float64 `json:"tilt,omitempty" jsonschema:"-1..1 (absolute)"`
	Zoom    float64 `json:"zoom,omitempty" jsonschema:"0..1 (absolute)"`
	VelPan  float64 `json:"vel_pan,omitempty" jsonschema:"-1..1 velocity (continuous)"`
	VelTilt float64 `json:"vel_tilt,omitempty" jsonschema:"-1..1 velocity (continuous)"`
	VelZoom float64 `json:"vel_zoom,omitempty" jsonschema:"-1..1 velocity (continuous)"`
}

type presetNameInput struct {
	Name string `json:"name,omitempty" jsonschema:"Preset name (optional for save; defaults to Preset_N)"`
}

type logsInput struct {
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum number of entries (default 50, max 500)"`
	Level string `json:"level,omitempty" jsonschema:"Only entries at this level or above: DEBUG, INFO, WARN, ERROR"`
}

type logLevelInput struct {
	Level string `json:"level" jsonschema:"DEBUG, INFO, WARN or ERROR"`
}

type hostInput struct {
	Host string `json:"host,omitempty" jsonschema:"Hostname or IP that clients use to reach MockCam (default localhost)"`
}

type snapshotInput struct {
	Token string `json:"token,omitempty" jsonschema:"Profile token (default Profile_1)"`
}

type listOutput[T any] struct {
	Items []T `json:"items"`
}

type presetsOutput struct {
	Presets []config.PTZPreset `json:"presets"`
}

type okOutput struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func registerTools(srv *mcp.Server, core *camera.Controller) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_status", Title: "Get camera status", Annotations: readOnly,
		Description: "Runtime status: version, uptime, RTSP client count, throughput, FFmpeg worker state and preview frame counters.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, camera.Status, error) {
		return nil, core.Status(), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_stream_urls", Title: "Get playback URLs", Annotations: readOnly,
		Description: "RTSP, JPEG snapshot, MJPEG and ONVIF URLs for every profile, ready to paste into VLC/ffplay or a VMS.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in hostInput) (*mcp.CallToolResult, listOutput[camera.StreamURLs], error) {
		return nil, listOutput[camera.StreamURLs]{Items: core.URLs(in.Host)}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_profiles", Title: "List stream profiles", Annotations: readOnly,
		Description: "All stream profiles with their video/audio settings.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, listOutput[config.ProfileConfig], error) {
		return nil, listOutput[config.ProfileConfig]{Items: core.Profiles()}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_profile", Title: "Get a stream profile", Annotations: readOnly,
		Description: "One profile by token.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in tokenInput) (*mcp.CallToolResult, config.ProfileConfig, error) {
		p, err := core.Profile(in.Token)
		return nil, p, toolErr(err)
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "create_profile", Title: "Create a stream profile", Annotations: mutating,
		Description: "Adds a profile and starts its FFmpeg worker. Token must match [A-Za-z0-9_-]{1,64}. Omitted numeric fields use FFmpeg defaults.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in profileInput) (*mcp.CallToolResult, okOutput, error) {
		if err := core.CreateProfile(in.Profile); err != nil {
			return nil, okOutput{}, toolErr(err)
		}
		return nil, okOutput{Status: "ok", Message: "profile created and worker started"}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "update_profile", Title: "Update a stream profile (hot reload)", Annotations: idempotent,
		Description: "Replaces a profile's settings and restarts only that profile's FFmpeg worker; other streams are not interrupted. Fetch the current profile first and send the full object back with your changes.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in updateProfileInput) (*mcp.CallToolResult, okOutput, error) {
		if err := core.UpdateProfile(in.Token, in.Profile); err != nil {
			return nil, okOutput{}, toolErr(err)
		}
		return nil, okOutput{Status: "ok", Message: "profile updated and worker restarted"}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "delete_profile", Title: "Delete a stream profile", Annotations: destructive,
		Description: "Stops the worker, closes the RTSP stream and removes the profile. The last remaining profile cannot be deleted.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in tokenInput) (*mcp.CallToolResult, okOutput, error) {
		if err := core.DeleteProfile(in.Token); err != nil {
			return nil, okOutput{}, toolErr(err)
		}
		return nil, okOutput{Status: "ok", Message: "profile deleted"}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_config", Title: "Get full configuration", Annotations: readOnly,
		Description: "The complete settings.json (server, profiles, ptz).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, config.Config, error) {
		return nil, core.Config().Get(), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "update_server_config", Title: "Update server settings", Annotations: idempotent,
		Description: "Ports, authentication (digest/basic/none), log level and ONVIF device information. Port changes apply to new workers/connections; a restart is recommended after changing ports.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in serverInput) (*mcp.CallToolResult, okOutput, error) {
		if err := core.UpdateServer(in.Server); err != nil {
			return nil, okOutput{}, toolErr(err)
		}
		return nil, okOutput{Status: "ok", Message: "server configuration saved"}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "factory_reset", Title: "Factory reset", Annotations: destructive,
		Description: "Restores the default configuration (two profiles, admin/admin1234) and restarts every worker.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, config.Config, error) {
		cfg, err := core.FactoryReset()
		return nil, cfg, toolErr(err)
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_ptz", Title: "Get PTZ position", Annotations: readOnly,
		Description: "Current virtual pan/tilt/zoom and whether a continuous move is in progress.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, camera.PTZState, error) {
		return nil, core.PTZState(), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "ptz_move", Title: "Move the camera (PTZ)", Annotations: idempotent,
		Description: "absolute: jump to pan/tilt (-1..1) and zoom (0..1). continuous: move with vel_* (10% of range per second at 1.0) until stop. stop: halt.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ptzInput) (*mcp.CallToolResult, camera.PTZState, error) {
		st, err := core.PTZMove(in.Action, in.Pan, in.Tilt, in.Zoom, in.VelPan, in.VelTilt, in.VelZoom)
		return nil, st, toolErr(err)
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_ptz_presets", Title: "List PTZ presets", Annotations: readOnly,
		Description: "Saved PTZ positions.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, presetsOutput, error) {
		return nil, presetsOutput{Presets: core.Presets()}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "save_ptz_preset", Title: "Save current position as preset", Annotations: idempotent,
		Description: "Stores the current pan/tilt/zoom under a name (overwrites an existing preset with the same name).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in presetNameInput) (*mcp.CallToolResult, presetsOutput, error) {
		presets, err := core.SavePreset(in.Name)
		return nil, presetsOutput{Presets: presets}, toolErr(err)
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "goto_ptz_preset", Title: "Go to a PTZ preset", Annotations: idempotent,
		Description: "Moves the camera to a saved preset.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in presetNameInput) (*mcp.CallToolResult, config.PTZPreset, error) {
		p, err := core.GotoPreset(in.Name)
		return nil, p, toolErr(err)
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "delete_ptz_preset", Title: "Delete a PTZ preset", Annotations: destructive,
		Description: "Removes a saved preset.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in presetNameInput) (*mcp.CallToolResult, presetsOutput, error) {
		presets, err := core.DeletePreset(in.Name)
		return nil, presetsOutput{Presets: presets}, toolErr(err)
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_snapshot", Title: "Get a JPEG snapshot", Annotations: readOnly,
		Description: "The most recent frame of a profile's live stream as a JPEG image (what the camera is sending right now, including clock/OSD overlays). Falls back to a synthetic preview until the encoder has produced a frame.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in snapshotInput) (*mcp.CallToolResult, any, error) {
		token := in.Token
		if token == "" {
			token = "Profile_1"
		}
		if _, err := core.Profile(token); err != nil {
			return nil, nil, toolErr(err)
		}
		data, source := core.Snapshot(token)
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("Snapshot of %s (%s, %d bytes)", token, source, len(data))},
				&mcp.ImageContent{Data: data, MIMEType: "image/jpeg"},
			},
		}, nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_clients", Title: "List connected RTSP clients", Annotations: readOnly,
		Description: "Active RTSP reader sessions with remote IP, stream and duration.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, listOutput[clientInfo], error) {
		clients := core.Clients()
		out := make([]clientInfo, 0, len(clients))
		for _, c := range clients {
			out = append(out, clientInfo{ID: c.ID, RemoteIP: c.RemoteIP, Path: c.Path, Transport: c.Transport, DurationSeconds: c.Duration})
		}
		return nil, listOutput[clientInfo]{Items: out}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_logs", Title: "Get recent logs", Annotations: readOnly,
		Description: "Recent log entries from MockCam and the FFmpeg workers, oldest first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in logsInput) (*mcp.CallToolResult, listOutput[logger.LogEntry], error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		if limit > 500 {
			limit = 500
		}
		entries := core.Logs(500)
		if in.Level != "" {
			entries = filterLevel(entries, logger.LogLevel(strings.ToUpper(in.Level)))
		}
		if len(entries) > limit {
			entries = entries[len(entries)-limit:]
		}
		return nil, listOutput[logger.LogEntry]{Items: entries}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "set_log_level", Title: "Set log level", Annotations: idempotent,
		Description: "Changes the minimum level of retained/broadcast log entries.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in logLevelInput) (*mcp.CallToolResult, okOutput, error) {
		lvl, err := core.SetLogLevel(in.Level)
		if err != nil {
			return nil, okOutput{}, toolErr(err)
		}
		return nil, okOutput{Status: "ok", Message: "log level set to " + string(lvl)}, nil
	})
}

type clientInfo struct {
	ID              string `json:"id"`
	RemoteIP        string `json:"remote_ip"`
	Path            string `json:"path"`
	Transport       string `json:"transport"`
	DurationSeconds int64  `json:"duration_seconds"`
}

func filterLevel(entries []logger.LogEntry, min logger.LogLevel) []logger.LogEntry {
	rank := map[logger.LogLevel]int{logger.LevelDebug: 1, logger.LevelInfo: 2, logger.LevelWarn: 3, logger.LevelError: 4}
	threshold, ok := rank[min]
	if !ok {
		return entries
	}
	out := entries[:0:0]
	for _, e := range entries {
		if rank[e.Level] >= threshold {
			out = append(out, e)
		}
	}
	return out
}

// toolErr converts controller errors into tool errors (reported to the model
// as isError results rather than protocol failures).
func toolErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, camera.ErrNotFound), errors.Is(err, camera.ErrInvalid), errors.Is(err, camera.ErrConflict):
		return err
	default:
		return fmt.Errorf("internal error: %w", err)
	}
}

// ---------------------------------------------------------------------------
// resources
// ---------------------------------------------------------------------------

const (
	uriConfig   = "mockcam://config"
	uriStatus   = "mockcam://status"
	uriLogs     = "mockcam://logs"
	uriOpenAPI  = "mockcam://openapi"
	uriLicenses = "mockcam://licenses"
	uriSnapshot = "mockcam://snapshot/{token}"
)

func registerResources(srv *mcp.Server, core *camera.Controller) {
	jsonResource := func(uri string, v func() any) mcp.ResourceHandler {
		return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			data, err := json.MarshalIndent(v(), "", "  ")
			if err != nil {
				return nil, err
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(data)}}}, nil
		}
	}

	srv.AddResource(&mcp.Resource{URI: uriConfig, Name: "config", Title: "Configuration (settings.json)", MIMEType: "application/json",
		Description: "The complete current configuration."},
		jsonResource(uriConfig, func() any { return core.Config().Get() }))

	srv.AddResource(&mcp.Resource{URI: uriStatus, Name: "status", Title: "Runtime status", MIMEType: "application/json",
		Description: "Same payload as the get_status tool."},
		jsonResource(uriStatus, func() any { return core.Status() }))

	srv.AddResource(&mcp.Resource{URI: uriLogs, Name: "logs", Title: "Recent logs", MIMEType: "application/json",
		Description: "The last 200 log entries."},
		jsonResource(uriLogs, func() any { return core.Logs(200) }))

	srv.AddResource(&mcp.Resource{URI: uriLicenses, Name: "licenses", Title: "Third-party licenses", MIMEType: "application/json",
		Description: "Attribution for bundled components."},
		jsonResource(uriLicenses, func() any { return licenses.Components }))

	srv.AddResource(&mcp.Resource{URI: uriOpenAPI, Name: "openapi", Title: "OpenAPI document", MIMEType: "application/yaml",
		Description: "OpenAPI 3.1 description of the REST API (also served at /openapi.yaml)."},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uriOpenAPI, MIMEType: "application/yaml", Text: string(docs.OpenAPI)}}}, nil
		})

	srv.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: uriSnapshot, Name: "snapshot", Title: "JPEG snapshot of a profile", MIMEType: "image/jpeg",
		Description: "Latest live frame of the given profile token."},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			token := strings.TrimPrefix(req.Params.URI, "mockcam://snapshot/")
			if _, err := core.Profile(token); err != nil {
				return nil, mcp.ResourceNotFoundError(req.Params.URI)
			}
			data, _ := core.Snapshot(token)
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "image/jpeg", Blob: data}}}, nil
		})
}

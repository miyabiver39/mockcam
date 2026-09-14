package config

// AppVersion represents the current software release version.
const AppVersion = "1.5.0"

// Resolution defines video width and height.
type Resolution struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// VideoConfig defines video stream settings.
type VideoConfig struct {
	Codec            string     `json:"codec"`
	Resolution       Resolution `json:"resolution"`
	Framerate        int        `json:"framerate"`
	GopSize          int        `json:"gop_size"`
	BitrateMode      string     `json:"bitrate_mode"` // "CBR" or "VBR"
	BitrateLimitKbps int        `json:"bitrate_limit_kbps"`
	Quality          float64    `json:"quality"`
	Pattern          string     `json:"pattern,omitempty"`  // "testsrc2", "smptebars", "allrgb", "mptestsrc"
	OsdText          string     `json:"osd_text,omitempty"` // Custom OSD text overlay
	ShowClock        bool       `json:"show_clock"`         // Whether to display clock overlay
	EnableNoise      bool       `json:"enable_noise"`       // Adds camera sensor grain/noise
	EnableMotionBox  bool       `json:"enable_motion_box"`  // Draws moving bounding box for VMS motion detection
}

// AudioConfig defines audio stream settings.
type AudioConfig struct {
	Enabled     bool   `json:"enabled"`
	Mode        string `json:"mode"` // "time_signal", "time_signal_ja", "time_signal_en", "silent", "noise", "chime"
	Codec       string `json:"codec"`
	BitrateKbps int    `json:"bitrate_kbps"`
	SampleRate  int    `json:"sample_rate"`
}

// ProfileConfig represents a stream profile.
type ProfileConfig struct {
	Token      string      `json:"token"`
	Name       string      `json:"name"`
	SourceMode string      `json:"source_mode"` // "generate" or "file"
	SourcePath string      `json:"source_path"`
	Video      VideoConfig `json:"video"`
	Audio      AudioConfig `json:"audio"`
}

// DeviceInfoConfig defines ONVIF device information.
type DeviceInfoConfig struct {
	Manufacturer    string `json:"manufacturer"`
	Model           string `json:"model"`
	FirmwareVersion string `json:"firmware_version"`
	SerialNumber    string `json:"serial_number"`
	HardwareID      string `json:"hardware_id"`
}

// ServerConfig defines server port and authentication settings.
type ServerConfig struct {
	RTSPPort   int              `json:"rtsp_port"`
	HTTPPort   int              `json:"http_port"`
	ONVIFPort  int              `json:"onvif_port"`
	AuthType   string           `json:"auth_type"` // "basic", "digest", or "none"
	AuthUser   string           `json:"auth_user"`
	AuthPass   string           `json:"auth_pass"`
	LogLevel   string           `json:"log_level,omitempty"` // "DEBUG", "INFO", "WARN", "ERROR"
	DeviceInfo DeviceInfoConfig `json:"device_info"`
}

// PTZPreset represents a saved PTZ coordinate.
type PTZPreset struct {
	Name string  `json:"name"`
	Pan  float64 `json:"pan"`
	Tilt float64 `json:"tilt"`
	Zoom float64 `json:"zoom"`
}

// PTZConfig defines PTZ node and initial position settings.
type PTZConfig struct {
	Enabled   bool        `json:"enabled"`
	NodeToken string      `json:"node_token"`
	Pan       float64     `json:"pan"`
	Tilt      float64     `json:"tilt"`
	Zoom      float64     `json:"zoom"`
	Speed     float64     `json:"speed,omitempty"`
	Presets   []PTZPreset `json:"presets,omitempty"`
}

// Config represents the complete MockCam settings.
type Config struct {
	Server   ServerConfig    `json:"server"`
	Profiles []ProfileConfig `json:"profiles"`
	PTZ      PTZConfig       `json:"ptz"`
}

// DefaultConfig returns the default configuration specified in the requirements.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			RTSPPort:  8554,
			HTTPPort:  8080,
			ONVIFPort: 3702,
			AuthType:  "digest",
			AuthUser:  "admin",
			AuthPass:  "admin1234",
			DeviceInfo: DeviceInfoConfig{
				Manufacturer:    "MockCam Standard",
				Model:           "MC-Pro-S",
				FirmwareVersion: AppVersion,
				SerialNumber:    "MC2026090001",
				HardwareID:      "v1.0",
			},
		},
		Profiles: []ProfileConfig{
			{
				Token:      "Profile_1",
				Name:       "MainStream-CBR-1080p",
				SourceMode: "generate",
				SourcePath: "",
				Video: VideoConfig{
					Codec:            "H264",
					Resolution:       Resolution{Width: 1920, Height: 1080},
					Framerate:        30,
					GopSize:          30,
					BitrateMode:      "CBR",
					BitrateLimitKbps: 4000,
					Quality:          5.0,
				},
				Audio: AudioConfig{
					Enabled:     true,
					Mode:        "time_signal",
					Codec:       "AAC",
					BitrateKbps: 128,
					SampleRate:  44100,
				},
			},
			{
				Token:      "Profile_2",
				Name:       "SubStream-VBR-720p",
				SourceMode: "generate",
				SourcePath: "",
				Video: VideoConfig{
					Codec:            "H264",
					Resolution:       Resolution{Width: 1280, Height: 720},
					Framerate:        15,
					GopSize:          30,
					BitrateMode:      "VBR",
					BitrateLimitKbps: 1000,
					Quality:          3.0,
				},
				Audio: AudioConfig{
					Enabled:     true,
					Mode:        "silent",
					Codec:       "AAC",
					BitrateKbps: 64,
					SampleRate:  44100,
				},
			},
		},
		PTZ: PTZConfig{
			Enabled:   true,
			NodeToken: "PTZNode_1",
			Pan:       0.0,
			Tilt:      0.0,
			Zoom:      0.0,
		},
	}
}

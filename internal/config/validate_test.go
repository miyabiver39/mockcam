package config

import (
	"strings"
	"testing"
)

func TestDefaultConfigIsValid(t *testing.T) {
	if err := Validate(*DefaultConfig()); err != nil {
		t.Fatalf("default config should validate: %v", err)
	}
	if DefaultConfig().Server.DeviceInfo.FirmwareVersion != AppVersion {
		t.Fatal("firmware version must track AppVersion")
	}
}

func TestValidateProfile(t *testing.T) {
	good := DefaultConfig().Profiles[0]

	cases := []struct {
		name   string
		mutate func(p *ProfileConfig)
		want   string // substring of the error, "" for valid
	}{
		{"default", func(*ProfileConfig) {}, ""},
		{"zero values use ffmpeg defaults", func(p *ProfileConfig) { *p = ProfileConfig{Token: "P"} }, ""},
		{"all codecs", func(p *ProfileConfig) { p.Video.Codec = "vp9"; p.Audio.Codec = "g711u" }, ""},
		{"empty token", func(p *ProfileConfig) { p.Token = "" }, "token"},
		{"token with slash", func(p *ProfileConfig) { p.Token = "a/b" }, "token"},
		{"token with space", func(p *ProfileConfig) { p.Token = "a b" }, "token"},
		{"bad source mode", func(p *ProfileConfig) { p.SourceMode = "rtsp" }, "source_mode"},
		{"file mode without path", func(p *ProfileConfig) { p.SourceMode = "file"; p.SourcePath = " " }, "source_path"},
		{"file mode with path", func(p *ProfileConfig) { p.SourceMode = "file"; p.SourcePath = "/media/a.mp4" }, ""},
		{"negative width", func(p *ProfileConfig) { p.Video.Resolution.Width = -1 }, "resolution"},
		{"half resolution", func(p *ProfileConfig) { p.Video.Resolution.Height = 0 }, "resolution"},
		{"16k", func(p *ProfileConfig) { p.Video.Resolution = Resolution{15360, 8640} }, "8K"},
		{"framerate too high", func(p *ProfileConfig) { p.Video.Framerate = 500 }, "framerate"},
		{"gop too high", func(p *ProfileConfig) { p.Video.GopSize = 5000 }, "gop_size"},
		{"bitrate negative", func(p *ProfileConfig) { p.Video.BitrateLimitKbps = -5 }, "bitrate_limit_kbps"},
		{"bitrate mode", func(p *ProfileConfig) { p.Video.BitrateMode = "ABR" }, "bitrate_mode"},
		{"video codec", func(p *ProfileConfig) { p.Video.Codec = "MPEG2" }, "video codec"},
		{"pattern", func(p *ProfileConfig) { p.Video.Pattern = "rgbtest" }, "pattern"},
		{"audio mode", func(p *ProfileConfig) { p.Audio.Mode = "music" }, "audio mode"},
		{"audio codec", func(p *ProfileConfig) { p.Audio.Codec = "MP3" }, "audio codec"},
		{"audio bitrate", func(p *ProfileConfig) { p.Audio.BitrateKbps = 5000 }, "bitrate_kbps"},
		{"sample rate", func(p *ProfileConfig) { p.Audio.SampleRate = 999999 }, "sample_rate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := good
			tc.mutate(&p)
			err := ValidateProfile(p)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v should mention %q", err, tc.want)
			}
		})
	}

	// Multiple problems are reported together.
	bad := ProfileConfig{Token: "", Video: VideoConfig{Framerate: -1, Codec: "X"}}
	err := ValidateProfile(bad)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, s := range []string{"token", "framerate", "video codec"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("joined error should mention %q: %v", s, err)
		}
	}
}

func TestValidateServer(t *testing.T) {
	good := DefaultConfig().Server

	cases := []struct {
		name   string
		mutate func(s *ServerConfig)
		want   string
	}{
		{"default", func(*ServerConfig) {}, ""},
		{"no auth without credentials", func(s *ServerConfig) { s.AuthType = "none"; s.AuthUser = ""; s.AuthPass = "" }, ""},
		{"basic", func(s *ServerConfig) { s.AuthType = "Basic" }, ""},
		{"log level", func(s *ServerConfig) { s.LogLevel = "debug" }, ""},
		{"rtsp port 0", func(s *ServerConfig) { s.RTSPPort = 0 }, "rtsp_port"},
		{"http port too high", func(s *ServerConfig) { s.HTTPPort = 70000 }, "http_port"},
		{"onvif port negative", func(s *ServerConfig) { s.ONVIFPort = -1 }, "onvif_port"},
		{"same ports", func(s *ServerConfig) { s.HTTPPort = s.RTSPPort }, "must differ"},
		{"auth type", func(s *ServerConfig) { s.AuthType = "oauth" }, "auth_type"},
		{"missing user", func(s *ServerConfig) { s.AuthUser = "" }, "auth_user"},
		{"missing pass", func(s *ServerConfig) { s.AuthPass = "" }, "auth_pass"},
		{"bad log level", func(s *ServerConfig) { s.LogLevel = "TRACE" }, "log_level"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := good
			tc.mutate(&s)
			err := ValidateServer(s)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v should mention %q", err, tc.want)
			}
		})
	}
}

func TestValidateWholeConfig(t *testing.T) {
	cfg := *DefaultConfig()

	cfg.Profiles = append(cfg.Profiles, cfg.Profiles[0])
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "duplicate token") {
		t.Fatalf("duplicate token not detected: %v", err)
	}

	cfg = *DefaultConfig()
	cfg.Profiles = nil
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "at least one profile") {
		t.Fatalf("empty profiles not detected: %v", err)
	}

	cfg = *DefaultConfig()
	cfg.PTZ.Zoom = 1.5
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "ptz") {
		t.Fatalf("ptz range not detected: %v", err)
	}

	cfg = *DefaultConfig()
	cfg.Profiles[1].Video.Framerate = -3
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "profiles[1]") {
		t.Fatalf("profile index missing from error: %v", err)
	}
}

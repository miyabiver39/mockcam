package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// tokenPattern restricts profile tokens to characters that are safe in RTSP
// paths, ONVIF tokens and shell-free FFmpeg arguments.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_\-]{1,64}$`)

// Allowed enumerations. Comparison is case-insensitive.
var (
	validAuthTypes   = []string{"digest", "basic", "none", ""}
	validSourceModes = []string{"generate", "file", ""}
	validVideoCodecs = []string{"H264", "H265", "HEVC", "VP9", "AV1", ""}
	validAudioCodecs = []string{"AAC", "G711A", "PCMA", "ALAW", "G711U", "PCMU", "MULAW", "G726", "ADPCM_G726", ""}
	validAudioModes  = []string{"time_signal", "time_signal_ja", "time_signal_en", "silent", "noise", "chime", ""}
	validPatterns    = []string{"testsrc2", "smptebars", "allrgb", "mptestsrc", ""}
	validBitrateMode = []string{"CBR", "VBR", ""}
)

func oneOf(value string, allowed []string) bool {
	for _, a := range allowed {
		if strings.EqualFold(strings.TrimSpace(value), a) {
			return true
		}
	}
	return false
}

func validPort(p int) bool { return p >= 1 && p <= 65535 }

// ValidateProfile checks a single profile for values that would make FFmpeg
// or the RTSP/ONVIF layers misbehave. Zero values that BuildFFmpegArgs
// substitutes with defaults are accepted.
func ValidateProfile(p ProfileConfig) error {
	var errs []error

	if !tokenPattern.MatchString(p.Token) {
		errs = append(errs, fmt.Errorf("token %q must match %s", p.Token, tokenPattern.String()))
	}
	if !oneOf(p.SourceMode, validSourceModes) {
		errs = append(errs, fmt.Errorf("source_mode %q is not supported", p.SourceMode))
	}
	if strings.EqualFold(p.SourceMode, "file") && strings.TrimSpace(p.SourcePath) == "" {
		errs = append(errs, errors.New("source_path is required when source_mode is \"file\""))
	}

	v := p.Video
	if v.Resolution.Width < 0 || v.Resolution.Height < 0 || (v.Resolution.Width == 0) != (v.Resolution.Height == 0) {
		errs = append(errs, fmt.Errorf("resolution %dx%d is invalid", v.Resolution.Width, v.Resolution.Height))
	}
	if v.Resolution.Width > 7680 || v.Resolution.Height > 4320 {
		errs = append(errs, fmt.Errorf("resolution %dx%d exceeds 8K", v.Resolution.Width, v.Resolution.Height))
	}
	if v.Framerate < 0 || v.Framerate > 240 {
		errs = append(errs, fmt.Errorf("framerate %d must be within 0..240", v.Framerate))
	}
	if v.GopSize < 0 || v.GopSize > 1000 {
		errs = append(errs, fmt.Errorf("gop_size %d must be within 0..1000", v.GopSize))
	}
	if v.BitrateLimitKbps < 0 || v.BitrateLimitKbps > 200000 {
		errs = append(errs, fmt.Errorf("bitrate_limit_kbps %d must be within 0..200000", v.BitrateLimitKbps))
	}
	if !oneOf(v.BitrateMode, validBitrateMode) {
		errs = append(errs, fmt.Errorf("bitrate_mode %q must be CBR or VBR", v.BitrateMode))
	}
	if !oneOf(v.Codec, validVideoCodecs) {
		errs = append(errs, fmt.Errorf("video codec %q is not supported", v.Codec))
	}
	if !oneOf(v.Pattern, validPatterns) {
		errs = append(errs, fmt.Errorf("pattern %q is not supported", v.Pattern))
	}

	a := p.Audio
	if !oneOf(a.Mode, validAudioModes) {
		errs = append(errs, fmt.Errorf("audio mode %q is not supported", a.Mode))
	}
	if !oneOf(a.Codec, validAudioCodecs) {
		errs = append(errs, fmt.Errorf("audio codec %q is not supported", a.Codec))
	}
	if a.BitrateKbps < 0 || a.BitrateKbps > 1024 {
		errs = append(errs, fmt.Errorf("audio bitrate_kbps %d must be within 0..1024", a.BitrateKbps))
	}
	if a.SampleRate < 0 || a.SampleRate > 192000 {
		errs = append(errs, fmt.Errorf("audio sample_rate %d must be within 0..192000", a.SampleRate))
	}

	return errors.Join(errs...)
}

// ValidateServer checks ports and authentication settings.
func ValidateServer(s ServerConfig) error {
	var errs []error
	for name, port := range map[string]int{"rtsp_port": s.RTSPPort, "http_port": s.HTTPPort, "onvif_port": s.ONVIFPort} {
		if !validPort(port) {
			errs = append(errs, fmt.Errorf("%s %d must be within 1..65535", name, port))
		}
	}
	if s.RTSPPort == s.HTTPPort {
		errs = append(errs, errors.New("rtsp_port and http_port must differ"))
	}
	if !oneOf(s.AuthType, validAuthTypes) {
		errs = append(errs, fmt.Errorf("auth_type %q must be digest, basic or none", s.AuthType))
	}
	if !strings.EqualFold(strings.TrimSpace(s.AuthType), "none") && strings.TrimSpace(s.AuthType) != "" {
		if strings.TrimSpace(s.AuthUser) == "" {
			errs = append(errs, errors.New("auth_user is required when authentication is enabled"))
		}
		if s.AuthPass == "" {
			errs = append(errs, errors.New("auth_pass is required when authentication is enabled"))
		}
	}
	if s.LogLevel != "" && !oneOf(s.LogLevel, []string{"DEBUG", "INFO", "WARN", "ERROR"}) {
		errs = append(errs, fmt.Errorf("log_level %q must be DEBUG, INFO, WARN or ERROR", s.LogLevel))
	}
	return errors.Join(errs...)
}

// Validate checks the whole configuration, including token uniqueness.
func Validate(c Config) error {
	var errs []error
	if err := ValidateServer(c.Server); err != nil {
		errs = append(errs, err)
	}
	if len(c.Profiles) == 0 {
		errs = append(errs, errors.New("at least one profile is required"))
	}
	seen := make(map[string]bool, len(c.Profiles))
	for i, p := range c.Profiles {
		if err := ValidateProfile(p); err != nil {
			errs = append(errs, fmt.Errorf("profiles[%d]: %w", i, err))
		}
		if seen[p.Token] {
			errs = append(errs, fmt.Errorf("profiles[%d]: duplicate token %q", i, p.Token))
		}
		seen[p.Token] = true
	}
	if c.PTZ.Pan < -1 || c.PTZ.Pan > 1 || c.PTZ.Tilt < -1 || c.PTZ.Tilt > 1 || c.PTZ.Zoom < 0 || c.PTZ.Zoom > 1 {
		errs = append(errs, errors.New("ptz pan/tilt must be within -1..1 and zoom within 0..1"))
	}
	return errors.Join(errs...)
}

package supervisor

import (
	"strings"
	"testing"

	"mockcam/internal/config"
)

func TestBuildFFmpegArgsCBR(t *testing.T) {
	prof := config.ProfileConfig{
		Token:      "Profile_1",
		Name:       "MainStream",
		SourceMode: "generate",
		Video: config.VideoConfig{
			Codec:            "H264",
			Resolution:       config.Resolution{Width: 1920, Height: 1080},
			Framerate:        30,
			GopSize:          30,
			BitrateMode:      "CBR",
			BitrateLimitKbps: 4000,
		},
		Audio: config.AudioConfig{
			Enabled:     true,
			Mode:        "time_signal",
			Codec:       "AAC",
			BitrateKbps: 128,
			SampleRate:  44100,
		},
	}

	args := BuildFFmpegArgs(prof, 8554)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "testsrc2=size=1920x1080:rate=30") {
		t.Errorf("expected testsrc2 filter in args: %s", joined)
	}
	if !strings.Contains(joined, "-b:v 4000k -minrate 4000k -maxrate 4000k -bufsize 8000k") {
		t.Errorf("expected CBR bitrate params in args: %s", joined)
	}
	if !strings.Contains(joined, "-c:v libx264") {
		t.Errorf("expected libx264 in args: %s", joined)
	}
	if !strings.Contains(joined, "sine=frequency=880:beep_factor=4:r=44100") {
		t.Errorf("expected time_signal audio in args: %s", joined)
	}
	if !strings.Contains(joined, "rtsp://127.0.0.1:8554/live/Profile_1") {
		t.Errorf("expected rtsp destination in args: %s", joined)
	}
}

func TestBuildFFmpegArgsVBR(t *testing.T) {
	prof := config.ProfileConfig{
		Token:      "Profile_2",
		Name:       "SubStream",
		SourceMode: "generate",
		Video: config.VideoConfig{
			Codec:            "H265",
			Resolution:       config.Resolution{Width: 1280, Height: 720},
			Framerate:        15,
			GopSize:          15,
			BitrateMode:      "VBR",
			BitrateLimitKbps: 1000,
		},
		Audio: config.AudioConfig{
			Enabled:    true,
			Mode:       "silent",
			Codec:      "AAC",
			SampleRate: 44100,
		},
	}

	args := BuildFFmpegArgs(prof, 8554)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "testsrc2=size=1280x720:rate=15") {
		t.Errorf("expected 720p 15fps in args: %s", joined)
	}
	if !strings.Contains(joined, "-crf 23 -maxrate 1000k -bufsize 2000k") {
		t.Errorf("expected VBR bitrate params in args: %s", joined)
	}
	if !strings.Contains(joined, "-c:v libx265") {
		t.Errorf("expected libx265 in args: %s", joined)
	}
	if !strings.Contains(joined, "anullsrc=channel_layout=stereo:sample_rate=44100") {
		t.Errorf("expected silent audio filter in args: %s", joined)
	}
}

func TestBuildFFmpegArgsCodecs(t *testing.T) {
	// Test VP9 and G711A
	profVP9 := config.ProfileConfig{
		Token: "Profile_VP9",
		Video: config.VideoConfig{
			Codec:       "VP9",
			Resolution:  config.Resolution{Width: 1920, Height: 1080},
			BitrateMode: "CBR",
		},
		Audio: config.AudioConfig{
			Enabled: true,
			Codec:   "G711A",
		},
	}
	argsVP9 := BuildFFmpegArgs(profVP9, 8554)
	joinedVP9 := strings.Join(argsVP9, " ")
	if !strings.Contains(joinedVP9, "-c:v libvpx-vp9") {
		t.Errorf("expected libvpx-vp9: %s", joinedVP9)
	}
	if !strings.Contains(joinedVP9, "-c:a pcm_alaw") {
		t.Errorf("expected pcm_alaw: %s", joinedVP9)
	}

	// Test AV1 and G726
	profAV1 := config.ProfileConfig{
		Token: "Profile_AV1",
		Video: config.VideoConfig{
			Codec:       "AV1",
			Resolution:  config.Resolution{Width: 1280, Height: 720},
			BitrateMode: "VBR",
		},
		Audio: config.AudioConfig{
			Enabled:     true,
			Codec:       "G726",
			BitrateKbps: 32,
		},
	}
	argsAV1 := BuildFFmpegArgs(profAV1, 8554)
	joinedAV1 := strings.Join(argsAV1, " ")
	if !strings.Contains(joinedAV1, "-c:v libsvtav1") {
		t.Errorf("expected libsvtav1: %s", joinedAV1)
	}
	if !strings.Contains(joinedAV1, "-c:a g726") {
		t.Errorf("expected g726: %s", joinedAV1)
	}
}

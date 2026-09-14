package supervisor

import (
	"fmt"
	"log"
	"os"
	"strings"

	"mockcam/internal/config"
)

// BuildFFmpegArgs generates the argument slice for FFmpeg based on profile configuration.
func BuildFFmpegArgs(profile config.ProfileConfig, rtspPort int) []string {
	var args []string

	// Global options: hide banner, loglevel warning
	args = append(args, "-hide_banner", "-loglevel", "warning")

	// 1. Video Input
	useFile := false
	if profile.SourceMode == "file" && strings.TrimSpace(profile.SourcePath) != "" {
		if _, err := os.Stat(profile.SourcePath); err == nil {
			useFile = true
		} else {
			log.Printf("[supervisor] Source file '%s' not found for profile '%s'; falling back to generate mode", profile.SourcePath, profile.Token)
		}
	}

	if useFile {
		args = append(args, "-re", "-stream_loop", "-1", "-i", profile.SourcePath)
	} else {
		// generate mode using testsrc2 + drawtext
		fps := profile.Video.Framerate
		if fps <= 0 {
			fps = 30
		}
		width := profile.Video.Resolution.Width
		height := profile.Video.Resolution.Height
		if width <= 0 || height <= 0 {
			width = 1920
			height = 1080
		}

		filter := fmt.Sprintf(
			"testsrc2=size=%dx%d:rate=%d,drawtext=text='%%{pts\\:hms}':x=(w-tw)/2:y=h-th-20:fontsize=32:fontcolor=white:box=1:boxcolor=black@0.6:boxborderw=5",
			width, height, fps,
		)
		args = append(args, "-re", "-f", "lavfi", "-i", filter)
	}

	// 2. Audio Input (if enabled)
	if profile.Audio.Enabled {
		sampleRate := profile.Audio.SampleRate
		if sampleRate <= 0 {
			sampleRate = 44100
		}
		if profile.Audio.Mode == "time_signal" {
			args = append(args, "-re", "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=880:beep_factor=4:r=%d", sampleRate))
		} else {
			// silent or default
			args = append(args, "-re", "-f", "lavfi", "-i", fmt.Sprintf("anullsrc=channel_layout=stereo:sample_rate=%d", sampleRate))
		}
	}

	// 3. Video Encoding & GOP
	gop := profile.Video.GopSize
	if gop <= 0 {
		gop = 30
	}
	args = append(args, "-g", fmt.Sprintf("%d", gop), "-keyint_min", fmt.Sprintf("%d", gop), "-sc_threshold", "0")

	// Bitrate
	kbps := profile.Video.BitrateLimitKbps
	if kbps <= 0 {
		kbps = 4000
	}
	if strings.EqualFold(profile.Video.BitrateMode, "CBR") {
		args = append(args,
			"-b:v", fmt.Sprintf("%dk", kbps),
			"-minrate", fmt.Sprintf("%dk", kbps),
			"-maxrate", fmt.Sprintf("%dk", kbps),
			"-bufsize", fmt.Sprintf("%dk", kbps*2),
		)
	} else {
		// VBR
		args = append(args,
			"-crf", "23",
			"-maxrate", fmt.Sprintf("%dk", kbps),
			"-bufsize", fmt.Sprintf("%dk", kbps*2),
		)
	}

	// Video Codec
	if strings.EqualFold(profile.Video.Codec, "H265") || strings.EqualFold(profile.Video.Codec, "HEVC") {
		args = append(args, "-c:v", "libx265", "-preset", "ultrafast", "-tune", "zerolatency", "-pix_fmt", "yuv420p")
	} else {
		args = append(args, "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-pix_fmt", "yuv420p")
	}

	// 4. Audio Encoding
	if profile.Audio.Enabled {
		audioBitrate := profile.Audio.BitrateKbps
		if audioBitrate <= 0 {
			audioBitrate = 128
		}
		sampleRate := profile.Audio.SampleRate
		if sampleRate <= 0 {
			sampleRate = 44100
		}
		args = append(args, "-c:a", "aac", "-b:a", fmt.Sprintf("%dk", audioBitrate), "-ar", fmt.Sprintf("%d", sampleRate))
	} else {
		args = append(args, "-an")
	}

	// 5. Output format & URL
	destURL := fmt.Sprintf("rtsp://127.0.0.1:%d/live/%s", rtspPort, profile.Token)
	args = append(args, "-rtsp_transport", "tcp", "-f", "rtsp", destURL)

	return args
}

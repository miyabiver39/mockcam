package supervisor

import (
	"fmt"
	"log"
	"os"
	"strings"

	"mockcam/internal/config"
	"mockcam/internal/timesignal"
)

// PreviewOptions controls the low-rate MJPEG side output that feeds the
// JPEG snapshot / MJPEG endpoints. Frames are written to FFmpeg's stdout as a
// raw MJPEG stream and reassembled by frames.Splitter.
type PreviewOptions struct {
	Enabled  bool
	FPS      int // frames per second of the preview stream (default 5)
	MaxWidth int // frames wider than this are downscaled (default 1280)
	Quality  int // MJPEG -q:v, 2 (best) .. 31 (worst); default 5
}

// DefaultPreview returns the preview settings used in production.
func DefaultPreview() PreviewOptions {
	return PreviewOptions{Enabled: true, FPS: 5, MaxWidth: 1280, Quality: 5}
}

// BuildFFmpegArgs generates the argument slice for FFmpeg based on profile
// configuration, including the default MJPEG preview side output.
func BuildFFmpegArgs(profile config.ProfileConfig, rtspPort int, httpPort ...int) []string {
	hPort := 8080
	if len(httpPort) > 0 && httpPort[0] > 0 {
		hPort = httpPort[0]
	}
	return BuildFFmpegArgsWith(profile, rtspPort, hPort, DefaultPreview())
}

// BuildFFmpegArgsWith is BuildFFmpegArgs with explicit ports and preview settings.
func BuildFFmpegArgsWith(profile config.ProfileConfig, rtspPort, hPort int, preview PreviewOptions) []string {
	if hPort <= 0 {
		hPort = 8080
	}

	var args []string

	// Global options: hide banner, loglevel warning, force timestamp generation & zero input latency
	args = append(args, "-hide_banner", "-loglevel", "warning", "-fflags", "+genpts+nobuffer")

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

		// Select generator pattern
		pattern := strings.ToLower(strings.TrimSpace(profile.Video.Pattern))
		if pattern == "" {
			pattern = "testsrc2"
		}
		var baseFilter string
		switch pattern {
		case "smptebars":
			baseFilter = fmt.Sprintf("smptebars=size=%dx%d:rate=%d", width, height, fps)
		case "allrgb":
			baseFilter = fmt.Sprintf("allrgb=rate=%d,scale=%d:%d", fps, width, height)
		case "mptestsrc":
			baseFilter = fmt.Sprintf("mptestsrc=rate=%d,scale=%d:%d", fps, width, height)
		default: // testsrc2
			baseFilter = fmt.Sprintf("testsrc2=size=%dx%d:rate=%d", width, height, fps)
		}

		filterParts := []string{baseFilter}

		// Noise/grain injection
		if profile.Video.EnableNoise {
			filterParts = append(filterParts, "noise=alls=12:allf=t")
		}

		// Moving motion bounding box for VMS motion detection testing
		if profile.Video.EnableMotionBox {
			// Box bounces horizontally across the frame
			filterParts = append(filterParts, fmt.Sprintf("drawbox=x='(w-160)*(0.5+0.5*sin(t*1.5))':y=60:w=160:h=120:color=red@0.8:t=4"))
		}

		// Real-time ISO 8601 clock overlay with millisecond precision (%3N)
		if profile.Video.ShowClock || profile.Video.OsdText == "" {
			filterParts = append(filterParts, "drawtext=text='%{localtime\\:%Y-%m-%dT%H\\\\\\:%M\\\\\\:%S.%3N%z}':x=(w-tw)/2:y=h-th-20:fontsize=32:fontcolor=white:box=1:boxcolor=black@0.6:boxborderw=5")
		}

		// Custom OSD text overlay
		if profile.Video.OsdText != "" {
			escapedText := strings.ReplaceAll(profile.Video.OsdText, ":", "\\:")
			escapedText = strings.ReplaceAll(escapedText, "'", "\\'")
			filterParts = append(filterParts, fmt.Sprintf("drawtext=text='%s':x=20:y=20:fontsize=28:fontcolor=cyan:box=1:boxcolor=black@0.6:boxborderw=4", escapedText))
		}

		// Timestamp normalization filter to avoid discontinuous PTS
		filterParts = append(filterParts, "setpts=PTS-STARTPTS")

		combinedFilter := strings.Join(filterParts, ",")
		args = append(args, "-re", "-f", "lavfi", "-i", combinedFilter)
	}

	// 2. Audio Input (if enabled)
	if profile.Audio.Enabled {
		sampleRate := profile.Audio.SampleRate
		if sampleRate <= 0 {
			sampleRate = 44100
		}
		audioMode := strings.ToLower(strings.TrimSpace(profile.Audio.Mode))
		switch audioMode {
		case "time_signal_ja", "time_signal_en":
			lang := "ja"
			if audioMode == "time_signal_en" {
				lang = "en"
			}
			args = append(args, "-re", "-f", "s16le", "-ar", fmt.Sprintf("%d", timesignal.SampleRate), "-ac", "1", "-i", fmt.Sprintf("http://127.0.0.1:%d/api/audio/timesignal?lang=%s", hPort, lang))
		case "time_signal":
			audioFilter := fmt.Sprintf("sine=frequency=880:beep_factor=4:r=%d,asetpts=PTS-STARTPTS", sampleRate)
			args = append(args, "-re", "-f", "lavfi", "-i", audioFilter)
		case "noise":
			audioFilter := fmt.Sprintf("anoisesrc=sample_rate=%d:amplitude=0.05,asetpts=PTS-STARTPTS", sampleRate)
			args = append(args, "-re", "-f", "lavfi", "-i", audioFilter)
		case "chime":
			audioFilter := fmt.Sprintf("sine=frequency=1046.5:beep_factor=2:r=%d,asetpts=PTS-STARTPTS", sampleRate)
			args = append(args, "-re", "-f", "lavfi", "-i", audioFilter)
		default:
			audioFilter := fmt.Sprintf("anullsrc=channel_layout=stereo:sample_rate=%d,asetpts=PTS-STARTPTS", sampleRate)
			args = append(args, "-re", "-f", "lavfi", "-i", audioFilter)
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

	// Video Codec & Flags for zero-latency streaming
	vCodec := strings.ToUpper(strings.TrimSpace(profile.Video.Codec))
	switch vCodec {
	case "H265", "HEVC":
		args = append(args, "-c:v", "libx265", "-preset", "ultrafast", "-tune", "zerolatency", "-pix_fmt", "yuv420p", "-x265-params", "bframes=0:no-scenecut=1")
	case "VP9":
		args = append(args, "-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-pix_fmt", "yuv420p")
	case "AV1":
		args = append(args, "-c:v", "libsvtav1", "-preset", "12", "-pix_fmt", "yuv420p")
	default: // H264 default
		args = append(args, "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-pix_fmt", "yuv420p", "-bf", "0", "-x264opts", "no-scenecut:bframes=0:force-cfr=1")
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

		aCodec := strings.ToUpper(strings.TrimSpace(profile.Audio.Codec))
		switch aCodec {
		case "G711A", "PCMA", "ALAW":
			args = append(args, "-c:a", "pcm_alaw", "-ar", "8000", "-ac", "1")
		case "G711U", "PCMU", "MULAW":
			args = append(args, "-c:a", "pcm_mulaw", "-ar", "8000", "-ac", "1")
		case "G726", "ADPCM_G726":
			args = append(args, "-c:a", "g726", "-b:a", "32k", "-ar", "8000", "-ac", "1")
		default: // AAC default
			args = append(args, "-c:a", "aac", "-b:a", fmt.Sprintf("%dk", audioBitrate), "-ar", fmt.Sprintf("%d", sampleRate))
		}
	} else {
		args = append(args, "-an")
	}

	// 5. Output format & URL with TCP interleaved buffer optimization
	// Note: using -rtpflags skip_rtcp prevents FFmpeg from sending conflicting sender reports to the RTSP proxy
	destURL := fmt.Sprintf("rtsp://127.0.0.1:%d/live/%s", rtspPort, profile.Token)
	args = append(args,
		"-rtsp_transport", "tcp",
		"-rtpflags", "skip_rtcp",
		"-buffer_size", "2048000",
		"-max_delay", "200000",
		"-f", "rtsp", destURL,
	)

	// 6. Optional MJPEG preview side output on stdout (video only, low rate).
	if preview.Enabled {
		args = append(args, previewArgs(preview)...)
	}

	return args
}

// previewArgs renders the second output that produces snapshot frames. The
// first output keeps FFmpeg's automatic stream selection; this one maps the
// video stream explicitly and drops audio.
func previewArgs(p PreviewOptions) []string {
	fps := p.FPS
	if fps <= 0 {
		fps = 5
	}
	maxWidth := p.MaxWidth
	if maxWidth <= 0 {
		maxWidth = 1280
	}
	quality := p.Quality
	if quality <= 0 {
		quality = 5
	}
	return []string{
		"-map", "0:v:0", "-an",
		// Keep the aspect ratio, never upscale, keep dimensions even for yuv420.
		"-vf", fmt.Sprintf("scale=w='min(%d,iw)':h=-2", maxWidth),
		"-r", fmt.Sprintf("%d", fps),
		// "-huffman default" forces the standard (ITU-T T.81 Annex K) Huffman
		// tables in every DHT segment. FFmpeg's mjpeg encoder otherwise emits
		// per-frame optimized tables, which some VMS/NVR decoders reject.
		"-c:v", "mjpeg", "-q:v", fmt.Sprintf("%d", quality), "-huffman", "default",
		"-pix_fmt", "yuvj420p",
		"-f", "mjpeg", "pipe:1",
	}
}

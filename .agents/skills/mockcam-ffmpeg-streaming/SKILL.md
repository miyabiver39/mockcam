---
name: mockcam-ffmpeg-streaming
description: >-
  Use this skill when modifying FFmpeg filtergraphs, video/audio codecs, test patterns,
  OSD text overlays, or RTSP streaming options in MockCam.
  Covers filter escaping rules, timestamp normalization, and audio synthesis quirks.
---

# MockCam FFmpeg & Streaming Pipeline Guide

This skill details the exact patterns, filter escaping rules, and known pitfalls when working with `internal/supervisor/ffmpeg_cmd.go` and RTSP streaming in MockCam.

## 1. Filtergraph Construction Rules

All video/audio generator args are built in `BuildFFmpegArgs(profile config.ProfileConfig, rtspPort int, httpPort ...int)`.

### Test Patterns & Scalers
Some FFmpeg source filters accept `size=WxH:rate=FPS`, while others **do not** accept `size`:
- `testsrc2`: accepts `size=WxH:rate=FPS`
- `smptebars`: accepts `size=WxH:rate=FPS`
- `allrgb`: **DOES NOT** accept `size`! Must use `allrgb=rate=FPS,scale=W:H`
- `mptestsrc`: **DOES NOT** accept `max_rate` or `size`! Must use `mptestsrc=rate=FPS,scale=W:H`

### OSD Escaping Rules (Drawtext Filter)
In FFmpeg's `expand_text` within `drawtext`, colons inside strftime format must be heavily escaped so that:
1. The lavfi filter parser does not treat them as option separators.
2. The `expand_text` parser does not treat them as strftime separators.

Example for ISO 8601 with millisecond precision:
```go
filterParts = append(filterParts, "drawtext=text='%{localtime\\:%Y-%m-%dT%H\\\\\\:%M\\\\\\:%S.%3N%z}':x=(w-tw)/2:y=h-th-20:fontsize=32:fontcolor=white:box=1:boxcolor=black@0.6:boxborderw=5")
```
- Note the `%3N` for millisecond format (3 digits).
- For custom text, escape colons and single quotes:
  ```go
  escaped := strings.ReplaceAll(text, ":", "\\:")
  escaped = strings.ReplaceAll(escaped, "'", "\\'")
  ```

### PTS Normalization
Always ensure `setpts=PTS-STARTPTS` (video) and `asetpts=PTS-STARTPTS` (audio) are appended to generated synthetic streams to avoid non-monotonic timestamp glitches that cause VMS / RTSP player drops.

## 2. Audio Pipeline & Modes

MockCam supports:
- `time_signal_ja` / `time_signal_en`: Streaming real-time PCM audio from internal HTTP endpoint `/api/audio/timesignal?lang=ja|en` via `-re -f s16le -ar 22050 -ac 1 -i http://127.0.0.1:<httpPort>/api/audio/timesignal?lang=<lang>`.
- `time_signal`: Pure 880Hz sine beeps via `sine=frequency=880:beep_factor=4:r=<sampleRate>`.
- `noise`: Pinkish noise via `anoisesrc=sample_rate=<sampleRate>:amplitude=0.05`.
- `chime`: 1046.5Hz bell chime via `sine=frequency=1046.5:beep_factor=2:r=<sampleRate>`.
- `silent`: Null audio stream via `anullsrc=channel_layout=stereo:sample_rate=<sampleRate>`.

## 3. RTSP Zero-Latency & Packet Dropping Prevention

To ensure smooth 1:N fanout without stuttering:
- Use `-rtsp_transport tcp` and `-rtpflags skip_rtcp` (prevents conflicting sender reports).
- Set `-buffer_size 2048000` and `-max_delay 200000`.
- For H.264: `-c:v libx264 -preset ultrafast -tune zerolatency -pix_fmt yuv420p -bf 0 -x264opts no-scenecut:bframes=0:force-cfr=1`.
- For H.265: `-c:v libx265 -preset ultrafast -tune zerolatency -pix_fmt yuv420p -x265-params bframes=0:no-scenecut=1`.
- For VP9: `-c:v libvpx-vp9 -deadline realtime -cpu-used 8 -pix_fmt yuv420p`.
- For AV1: `-c:v libsvtav1 -preset 12 -pix_fmt yuv420p`.

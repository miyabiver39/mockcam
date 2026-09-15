# MockCam - Virtual Network Camera Emulator

**English** | [日本語](README.jp.md)

[![CI/CD Pipeline](https://github.com/miyabiver39/mockcam/actions/workflows/ci.yml/badge.svg)](https://github.com/miyabiver39/mockcam/actions)
[![Release](https://img.shields.io/github/v/release/miyabiver39/mockcam?include_prereleases&color=06b6d4)](https://github.com/miyabiver39/mockcam/releases)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go)](go.mod)
[![Docker](https://img.shields.io/badge/Docker-Multi--Arch-2496ED?logo=docker)](Dockerfile)

`MockCam` is a highly scalable **virtual network camera emulator** built for developing and load-testing VMS (video management systems), NVRs and other surveillance-camera integrations.

A fully static Go binary and the 1:N zero-copy fan-out of `gortsplib/v5` let a single process serve **1,000+ concurrent RTSP clients** with low CPU load and low latency.

---

## 🌟 Feature Highlights

* **🚀 1,000+ concurrent RTSP clients**
  * `gortsplib/v5` fans RTP packets from FFmpeg out to every reader without blocking.
* **🎥 Multiple profiles with hot reload**
  * Manage several profiles independently — e.g. a 1080p CBR main stream and a 720p VBR sub stream.
  * Changing a profile restarts **only that profile's** FFmpeg subprocess (`SIGTERM`, then `Kill` if it does not exit); other streams are never interrupted.
  * Crashed FFmpeg workers are restarted automatically.
* **📷 JPEG snapshots & MJPEG preview of the live stream**
  * `GET /api/snapshot/{token}` returns the latest frame of what is actually being sent over RTSP (test pattern, clock and OSD overlays included) — the URL advertised by ONVIF `GetSnapshotUri`.
  * `GET /api/mjpeg/{token}` streams the same frames as `multipart/x-mixed-replace` (~5 fps), usable directly in an `<img>` tag or as an MJPEG camera in a VMS. Video only, no audio.
* **🎞️ Test patterns & overlays**
  * Pick `testsrc2` / `smptebars` / `allrgb` / `mptestsrc`, or loop a video file (`source_mode: "file"`).
  * Overlay an ISO 8601 clock with milliseconds, custom OSD text, sensor noise, and a moving bounding box for motion-detection tests.
* **⏱️ Time-signal audio (beep or spoken "117" clock)**
  * 880 Hz beep (`time_signal`), silence (`silent`), white noise (`noise`), chime (`chime`), plus a **spoken speaking clock** (`time_signal_ja` / `time_signal_en`).
  * Japanese is synthesised with Open JTalk (tohoku-f01 female voice), English with espeak-ng / Windows SAPI. Every 10 seconds the upcoming mark is announced, followed by the tone pattern "pi-po-pi-po-pi-po-pi-pi-pi—" (880/440 Hz pips, 880 Hz mark).
* **📡 ONVIF Profile S**
  * **WS-Discovery** on UDP 3702 (multicast `239.255.255.250`) for automatic detection.
  * **SOAP services**: Device, Media and PTZ services (`GetDeviceInformation`, `GetCapabilities`, `GetServices`, `GetNetworkInterfaces`, `GetProfiles`, `GetStreamUri`, `GetSnapshotUri`, encoder/source configurations, PTZ moves, home position and presets, ...).
  * **WS-Security UsernameToken** (PasswordDigest / PasswordText) as sent by ONVIF Device Manager, VMS/NVR software and `onvif-zeep`, in addition to HTTP Basic / Digest.
* **🧭 Real-time virtual PTZ state machine**
  * Pan/tilt/zoom kept in memory and driven by VMS PTZ commands or the Web UI; two-way live sync with the SVG radar in the dashboard over WebSocket (`/ws`).
  * PTZ presets are saved to `settings.json` and shared between the REST API, MCP and ONVIF (`SetPreset` / `GotoPreset`).
* **💻 Embedded Web dashboard, no build step**
  * Tailwind CSS + Alpine.js dashboard embedded in the binary with Go's `embed`; 7 UI languages.
  * Live diagnostic log (including FFmpeg stderr), connected RTSP clients, config export / factory reset, one-click diagnostics bundle, third-party license notice.
* **📖 OpenAPI 3.1 + Scalar API reference**
  * The management API is described in [`docs/openapi.yaml`](docs/openapi.yaml), served at `/openapi.yaml`, and rendered interactively at `/api/docs`.
* **🤖 MCP (Model Context Protocol) server**
  * AI agents (Claude Desktop, Claude Code, Cursor, …) can inspect and control the camera: `/mcp` (Streamable HTTP) or `mockcam -mcp-stdio`. Tools mirror the REST API and `get_snapshot` returns the live JPEG as image content.

---

## 🚀 Quick Start

### 1. Docker Compose (recommended)

```bash
docker compose up -d
```

### 2. Docker Run

```bash
docker run -d \
  --name mockcam \
  -p 8554:8554 \
  -p 8080:8080 \
  -p 3702:3702/udp \
  -v $(pwd)/config:/config \
  ghcr.io/miyabiver39/mockcam:latest
```

### 3. Build & run locally (Go 1.26+)

With FFmpeg installed you can build and run the binary directly.

```bash
go mod download
go build -o mockcam ./cmd/mockcam
./mockcam

# explicit config path
./mockcam -config ./config/settings.json
```

> The configuration file is resolved in this order: `-config` flag → `CONFIG_PATH` environment variable → default path (Linux/macOS: `/config/settings.json`, Windows: `./config/settings.json`). A default configuration is generated when the file does not exist.
>
> On Windows the WS-Discovery multicast listener may fail to bind; MockCam logs a warning and keeps running (RTSP / Web / SOAP remain available). Local runs need `ffmpeg` on `PATH`; the spoken clock additionally needs Open JTalk (Japanese) or espeak-ng / SAPI (English) and otherwise falls back to a chime.

---

## 📡 Connecting

### 1. Web dashboard

```text
http://localhost:8080
```

* **Status panel** — uptime, connected RTSP clients, packets / bitrate, worker state.
* **Profile management** — resolution, FPS, GOP, bitrate (CBR/VBR), test pattern, OSD text, audio mode; changes hot-reload instantly. Add and delete profiles.
* **PTZ radar & presets** — drive the camera with the D-pad, sliders or keyboard (`W/A/S/D`, arrows, `+/-`, `Space`) and save positions as presets.
* **Live preview** — MJPEG stream of the real camera output, or periodic JPEG snapshots.
* **Diagnostics** — live console log, connected clients, `settings.json` export, factory reset, downloadable diagnostics bundle.
* **ℹ️ About** — version and third-party licenses; **API Reference** (Scalar) and **OpenAPI** links in the footer.

### 2. VLC / ffplay

Default credentials are `admin` / `admin1234`.

```bash
# main stream (1080p 30fps CBR)
ffplay rtsp://admin:admin1234@localhost:8554/live/Profile_1

# sub stream (720p 15fps VBR)
ffplay rtsp://admin:admin1234@localhost:8554/live/Profile_2
```

### 3. JPEG snapshot / MJPEG

```bash
# single JPEG of the current picture (what RTSP clients see)
curl -o snap.jpg http://localhost:8080/api/snapshot/Profile_1

# MJPEG stream (~5 fps), e.g. in a browser or VMS "MJPEG camera" input
ffplay http://localhost:8080/api/mjpeg/Profile_1
```

The `X-MockCam-Source` response header is `live` for encoder frames and `synthetic` while the worker has not produced a frame yet (e.g. right after start-up or when FFmpeg is missing).

### 4. VMS / ONVIF Device Manager

1. Start your VMS or `ONVIF Device Manager (ODM)` on the same network.
2. MockCam appears automatically via WS-Discovery (UDP 3702).
3. Manual service address:
   ```text
   http://<Host-IP>:8080/onvif/device_service
   ```
4. Credentials: `admin` / `admin1234`, sent either as a WS-Security UsernameToken in the SOAP header (what ODM and most VMS do) or as HTTP Basic / Digest. `GetSnapshotUri` returns the JPEG endpoint above.
5. Clients must have their clock within 5 minutes of MockCam for `PasswordDigest` tokens; `GetSystemDateAndTime` is unauthenticated so they can synchronise first.

### 5. MCP (AI agents)

Streamable HTTP — add to your MCP client configuration:

```json
{
  "mcpServers": {
    "mockcam": { "type": "http", "url": "http://localhost:8080/mcp" }
  }
}
```

stdio — let the client spawn MockCam itself (the camera runs for the lifetime of the session, logs go to stderr):

```json
{
  "mcpServers": {
    "mockcam": { "command": "mockcam", "args": ["-mcp-stdio", "-config", "./config/settings.json"] }
  }
}
```

Tools: `get_status`, `get_stream_urls`, `list_profiles`, `get_profile`, `create_profile`, `update_profile`, `delete_profile`, `get_config`, `update_server_config`, `factory_reset`, `get_ptz`, `ptz_move`, `list_ptz_presets`, `save_ptz_preset`, `goto_ptz_preset`, `delete_ptz_preset`, `get_snapshot`, `list_clients`, `get_logs`, `set_log_level`.
Resources: `mockcam://config`, `mockcam://status`, `mockcam://logs`, `mockcam://licenses`, `mockcam://openapi`, `mockcam://snapshot/{token}`.

---

## ⚙️ Configuration (`settings.json`)

The configuration lives at `/config/settings.json` (`./config/settings.json` on Windows, or the path given by `-config` / `CONFIG_PATH`). Changes made through the Web UI, REST API or MCP are written back immediately.

Default configuration generated on first start (main / sub profile):

```json
{
  "server": {
    "rtsp_port": 8554,
    "http_port": 8080,
    "onvif_port": 3702,
    "auth_type": "digest",
    "auth_user": "admin",
    "auth_pass": "admin1234",
    "device_info": {
      "manufacturer": "MockCam Standard",
      "model": "MC-Pro-S",
      "firmware_version": "1.6.0",
      "serial_number": "MC2026090001",
      "hardware_id": "v1.0"
    }
  },
  "profiles": [
    {
      "token": "Profile_1",
      "name": "MainStream-CBR-1080p",
      "source_mode": "generate",
      "source_path": "",
      "video": {
        "codec": "H264",
        "resolution": { "width": 1920, "height": 1080 },
        "framerate": 30,
        "gop_size": 30,
        "bitrate_mode": "CBR",
        "bitrate_limit_kbps": 4000,
        "quality": 5,
        "show_clock": false,
        "enable_noise": false,
        "enable_motion_box": false
      },
      "audio": {
        "enabled": true,
        "mode": "time_signal",
        "codec": "AAC",
        "bitrate_kbps": 128,
        "sample_rate": 44100
      }
    },
    {
      "token": "Profile_2",
      "name": "SubStream-VBR-720p",
      "source_mode": "generate",
      "source_path": "",
      "video": {
        "codec": "H264",
        "resolution": { "width": 1280, "height": 720 },
        "framerate": 15,
        "gop_size": 30,
        "bitrate_mode": "VBR",
        "bitrate_limit_kbps": 1000,
        "quality": 3,
        "show_clock": false,
        "enable_noise": false,
        "enable_motion_box": false
      },
      "audio": {
        "enabled": true,
        "mode": "silent",
        "codec": "AAC",
        "bitrate_kbps": 64,
        "sample_rate": 44100
      }
    }
  ],
  "ptz": {
    "enabled": true,
    "node_token": "PTZNode_1",
    "pan": 0,
    "tilt": 0,
    "zoom": 0
  }
}
```

### Settings reference

#### `server`

| Key | Description | Default |
|---|---|---|
| `rtsp_port` | RTSP listen port | `8554` |
| `http_port` | Web UI, REST API, SOAP, MCP listen port | `8080` |
| `onvif_port` | WS-Discovery multicast port | `3702` |
| `auth_type` | Authentication (`digest`, `basic`, `none`); applies to RTSP and ONVIF SOAP | `digest` |
| `auth_user` / `auth_pass` | Credentials | `admin` / `admin1234` |
| `log_level` | `DEBUG`, `INFO`, `WARN`, `ERROR` (default `INFO` when omitted) | (omitted) |
| `device_info.*` | Manufacturer, model, firmware, serial and hardware ID returned by ONVIF `GetDeviceInformation`. `firmware_version` is always synced to the running MockCam version | see above |

#### `profiles[]`

| Key | Description | Default |
|---|---|---|
| `token` | Identifier (`[A-Za-z0-9_-]{1,64}`); used in the RTSP path `/live/<token>` and as the ONVIF profile token | `Profile_1` |
| `name` | Display name | `MainStream-CBR-1080p` |
| `source_mode` | `generate` (FFmpeg test pattern) or `file` (loop a video file) | `generate` |
| `source_path` | Video file for `file` mode (under `/media` in the container); falls back to `generate` when missing | `""` |
| `video.codec` | `H264`, `H265`, `VP9`, `AV1` | `H264` |
| `video.resolution` | `width` / `height` | `1920x1080` |
| `video.framerate` | Frames per second | `30` |
| `video.gop_size` | GOP length (keyframe interval) | `30` |
| `video.bitrate_mode` | `CBR` or `VBR` | `CBR` |
| `video.bitrate_limit_kbps` | Bitrate (fixed for CBR, `maxrate` for VBR) | `4000` |
| `video.quality` | Reserved (not used by the encoder yet) | `5` |
| `video.pattern` | Test pattern (`testsrc2`, `smptebars`, `allrgb`, `mptestsrc`), `generate` mode only | `testsrc2` |
| `video.osd_text` | Custom overlay text (top-left) | `""` |
| `video.show_clock` | Draw the ISO 8601 clock with milliseconds at the bottom (always drawn when `osd_text` is empty) | `false` |
| `video.enable_noise` | Add sensor grain | `false` |
| `video.enable_motion_box` | Draw a moving red box for motion-detection tests | `false` |
| `audio.enabled` | Whether an audio track is present | `true` |
| `audio.mode` | `time_signal` (880 Hz beep), `time_signal_ja` / `time_signal_en` (spoken 117 clock + tones), `silent`, `noise`, `chime` | `time_signal` |
| `audio.codec` | `AAC`, `G711A` (PCMA), `G711U` (PCMU), `G726` | `AAC` |
| `audio.bitrate_kbps` / `audio.sample_rate` | Audio bitrate (kbps) / sample rate (Hz) | `128` / `44100` |

#### `ptz`

| Key | Description | Default |
|---|---|---|
| `enabled` | Enable the PTZ service | `true` |
| `node_token` | ONVIF PTZ node token | `PTZNode_1` |
| `pan` / `tilt` | Virtual position (`-1.0`–`1.0`); the current position is persisted on every move | `0` |
| `zoom` | Virtual zoom (`0.0`–`1.0`) | `0` |
| `speed` | Reserved (ContinuousMove moves 10 % of the range per second at velocity 1.0) | (omitted) |
| `presets[]` | Saved presets `{ "name", "pan", "tilt", "zoom" }`, managed by the Web UI / `/api/ptz/presets` / MCP | (omitted) |

---

## 🔊 How the spoken "117" clock works

With `audio.mode` set to `time_signal_ja` / `time_signal_en`, FFmpeg reads 48 kHz / 16-bit / mono PCM from the internal endpoint `/api/audio/timesignal?lang=ja|en`. Each 10-second block sounds like this:

| Second (within the block) | Content |
|---|---|
| `:01` – `:03` | Announcement of the upcoming mark (e.g. "25分30秒をお知らせします"; on the minute "午後3時25分をお知らせします" / "At the tone, the time will be …") |
| `:01` `:03` `:05` | "pi" — 880 Hz pip, 100 ms |
| `:02` `:04` `:06` | "po" — 440 Hz pip, 100 ms |
| `:07` `:08` `:09` | "pi pi pi" — 880 Hz preview pips |
| `:00` | mark — 880 Hz, 800 ms with a bell-like decay |

All tone edges use raised-cosine ramps (no clicks) and the mix never clips.

### Open JTalk parameter policy

The bundled HTS voices (tohoku-f01 / nitech) are trained at **48 kHz**. Overriding only the sampling rate (`-s`) without the matching all-pass constant (`-a`) warps the spectral envelope and produces a hollow, unnatural voice. Since v1.4.0:

* only `open_jtalk -x <dic> -m <voice> -r 1.00 -ow <wav>` is passed — sampling rate, α and pitch (`-fm`) stay at the model defaults;
* the WAV is parsed chunk by chunk in Go and resampled only if necessary;
* silence trimming, 10 ms fades and peak normalisation (−4.4 dBFS) keep the voice clean when mixed with the tones.

Engine order: Open JTalk (Japanese) → Windows SAPI → espeak-ng → chime fallback. Override the dictionary / voice locations with `MOCKCAM_OPENJTALK_DIC` / `MOCKCAM_OPENJTALK_VOICE`.

---

## ⚡ Performance tuning (1,000+ clients)

When load-testing with more than 1,000 concurrent clients, raise the file-descriptor limit and tune the Linux kernel on the host.

### 1. File descriptors
`/etc/security/limits.conf`:
```text
* soft nofile 65535
* hard nofile 65535
```
or start the container with `--ulimit nofile=65535:65535`.

### 2. Sockets & kernel parameters
Add to `/etc/sysctl.conf` and run `sysctl -p`:
```ini
net.core.somaxconn = 65535
net.ipv4.tcp_max_syn_backlog = 8192
net.ipv4.ip_local_port_range = 1024 65535
net.ipv4.tcp_tw_reuse = 1
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
```

---

## 📖 Documentation

* [OpenAPI 3.1 specification](docs/openapi.yaml) — served at `/openapi.yaml`, browse it at `/api/docs` (Scalar)
* [REST API & WebSocket notes (Japanese)](docs/rest_api.md)
* [ONVIF Profile S details (Japanese)](docs/onvif_profile_s.md)
* [Guide for AI coding agents](AGENTS.md) (Claude Code reads `CLAUDE.md`, GitHub Copilot reads `.github/copilot-instructions.md`)

---

## 📄 License

MockCam is released under the [MIT License](LICENSE).

### Third-party components

The following third-party components are bundled with or used by MockCam. The same list is available from the dashboard's "ℹ️ About" dialog and `GET /api/licenses` (`internal/licenses` is the single source of truth).

| Component | License | Copyright |
|---|---|---|
| [gortsplib/v5](https://github.com/bluenviron/gortsplib) | MIT | bluenviron |
| [gorilla/websocket](https://github.com/gorilla/websocket) | BSD-2-Clause | The Gorilla WebSocket Authors |
| [google/uuid](https://github.com/google/uuid) | BSD-3-Clause | Google LLC |
| [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk), google/jsonschema-go | MIT | The Go MCP SDK Authors / The Go JSON Schema Authors |
| [pion/rtp](https://github.com/pion/rtp), [pion/rtcp](https://github.com/pion/rtcp), pion/sdp, pion/srtp, pion/transport | MIT | The Pion community |
| [bluenviron/mediacommon](https://github.com/bluenviron/mediacommon) | MIT | bluenviron |
| segmentio/encoding, yosida95/uritemplate | MIT / BSD-3-Clause | Segment.io, Inc. / Kohei YOSHIDA |
| go.yaml.in/yaml/v3 | MIT / Apache-2.0 | Kirill Simonov, Canonical Ltd |
| golang.org/x/net, x/sys, x/sync, x/time, x/oauth2, Go standard library | BSD-3-Clause | The Go Authors |
| [Tailwind CSS](https://github.com/tailwindlabs/tailwindcss) (Play CDN) | MIT | Tailwind Labs, Inc. |
| [Alpine.js](https://github.com/alpinejs/alpine) | MIT | Caleb Porzio and contributors |
| [Scalar API Reference](https://github.com/scalar/scalar) (CDN, `/api/docs`) | MIT | Scalar |
| [FFmpeg](https://ffmpeg.org/) (external process) | GPL-2.0-or-later / LGPL-2.1-or-later | the FFmpeg developers |
| [espeak-ng](https://github.com/espeak-ng/espeak-ng) (external process) | GPL-3.0-or-later | eSpeak NG contributors |
| [Open JTalk](https://open-jtalk.sourceforge.net/) | Modified BSD | Copyright (C) 2008-2016 Nagoya Institute of Technology |
| [HTS Engine API](https://hts-engine.sourceforge.net/) | Modified BSD | Copyright (C) 2001-2015 Nagoya Institute of Technology / Tokyo Institute of Technology |
| NAIST-jdic (open_jtalk_dic_utf_8-1.11) | BSD-3-Clause | Copyright (C) 2009 Nara Institute of Science and Technology |
| HTS Voice tohoku-f01-neutral | [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/) | Tohoku University, Graduate School of Information Sciences |
| [DejaVu Fonts](https://dejavu-fonts.github.io/) | Bitstream Vera License / Public Domain | Bitstream, Inc. / DejaVu contributors |

> **HTS Voice tohoku-f01-neutral (CC BY 4.0) attribution**:
> This product uses the HTS voice model `tohoku-f01-neutral` created by the Tohoku University, Graduate School of Information Sciences, licensed under the [Creative Commons Attribution 4.0 International License](https://creativecommons.org/licenses/by/4.0/).

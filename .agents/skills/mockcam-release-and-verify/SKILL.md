---
name: mockcam-release-and-verify
description: >-
  Use this skill when developing, testing, containerizing, or creating a release for MockCam.
  It covers version synchronization across files, unit testing without FFmpeg, Docker multi-arch builds,
  container runtime verification, and GitHub release automation via the gh CLI.
---

# MockCam Release and Verification Workflow

This skill provides the standard runbook for building, testing, verifying, and releasing new versions of MockCam.

## 1. Version Synchronization Checklist

When releasing a new version, bump the version string across all required locations:

1. **`internal/config/types.go`**:
   - Update `AppVersion = "X.Y.Z"`
   - `DefaultConfig().Server.DeviceInfo.FirmwareVersion` uses `AppVersion`; existing `settings.json` files are re-synced on load.
2. **`internal/web/static/index.html`**:
   - Update the two fallback version strings (`'vX.Y.Z'` in the header badge and the footer).
3. **`internal/licenses/licenses.go`**:
   - Update `Version` fields when Go modules were bumped (`licenses_test.go` cross-checks go.mod).
4. **`README.md`** & **`docs/`** (if applicable):
   - Ensure documented features and version badge/mentions reflect the release.
5. **Toolchain**: `go.mod` (`go 1.26`), `Dockerfile` (`golang:1.26-alpine`, `alpine:3.22`) and `.github/workflows/ci.yml` (`go-version: '1.26.x'`, Node 24 based actions) must stay consistent.

## 2. Local Static Analysis & Unit Tests

Run these checks to ensure code quality without requiring FFmpeg:

```bash
# Static check
go vet ./...

# Formatting check (CI fails on any output)
gofmt -l .

# Format any unformatted files
gofmt -w internal/ cmd/

# Dependency hygiene (CI fails if go.mod/go.sum change)
go mod tidy

# Vulnerability scan (same as CI)
go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./...

# Unit tests (MockCam unit tests are designed to run without FFmpeg)
go test -race -count=1 ./...
```

> [!IMPORTANT]
> Tests never spawn the real `ffmpeg` binary and never bind fixed network ports.
> - `internal/supervisor`: `BuildFFmpegArgs` is a pure function; process lifecycle tests use `WithCommandFactory` with the Go test binary as a fake FFmpeg (helper-process pattern).
> - `internal/timesignal`: TTS backends take a `Runner` interface; tests use a fake runner that writes a tiny WAV.
> - `internal/web`: handlers take `StreamSupervisor` / `StreamServer` interfaces; tests use fakes and `httptest`.
> - `internal/rtsp`: authentication is a pure function (`authorizeRequest`); no RTSP sockets are opened.

## 3. Docker Image Build & Verification

Rebuild the local container image and run an integration smoke test:

```bash
# Build local docker image
docker build -t mockcam:latest .

# Stop any previous test container
docker rm -f mockcam-test 2>/dev/null || true

# Run test container
docker run -d --name mockcam-test \
  -p 8080:8080 -p 8554:8554 -p 3702:3702/udp \
  mockcam:latest

# Check startup logs
docker logs mockcam-test
```

### Verification Checks

1. **Status API**:
   ```bash
   curl -s http://localhost:8080/api/status
   ```
   Confirm `"version": "X.Y.Z"` matches and `rtsp_clients` is operational.

2. **Snapshot Endpoint**:
   ```bash
   curl -s -I http://localhost:8080/api/snapshot/Profile_1
   ```
   Must return `HTTP/1.1 200 OK` and `Content-Type: image/jpeg`.

3. **Favicon Assets**:
   ```bash
   curl -s -I http://localhost:8080/favicon.png
   ```

4. **Web UI Access**:
   Ensure http://localhost:8080 loads the Alpine.js dashboard without JS console errors.

## 4. Git Commit & Release Tagging

Use Conventional Commits and tag via `gh`:

```bash
# Stage changes
git add .

# Commit
git commit -m "feat: <description>"

# Push to origin
git push origin main

# Create GitHub release
gh release create vX.Y.Z \
  --title "vX.Y.Z - <Title Summary>" \
  --notes "<Release notes bullets>"
```

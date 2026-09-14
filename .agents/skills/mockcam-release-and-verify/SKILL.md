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
   - Check `DefaultConfig().Server.DeviceInfo.FirmwareVersion` uses `AppVersion`.
2. **`internal/web/static/index.html`**:
   - Update fallback version string in Alpine.js `status.version || 'X.Y.Z'`.
3. **`README.md`** & **`docs/`** (if applicable):
   - Ensure documented features and version badge/mentions reflect the release.

## 2. Local Static Analysis & Unit Tests

Run these checks to ensure code quality without requiring FFmpeg:

```bash
# Static check
go vet ./...

# Formatting check
gofmt -l .

# Format any unformatted files
gofmt -w internal/

# Unit tests (MockCam unit tests are designed to run without FFmpeg)
go test -v ./...
```

> [!IMPORTANT]
> All unit tests in `internal/supervisor` must use `BuildFFmpegArgs` as a pure function. Never add tests that attempt to spawn real FFmpeg processes or bind network sockets.

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

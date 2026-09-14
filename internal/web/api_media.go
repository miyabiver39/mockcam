package web

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	snapshotMaxWidth  = 1280
	snapshotMaxHeight = 720
	mjpegFrameDelay   = 66 * time.Millisecond // ~15 fps
)

// snapshotSize returns the preview size for a profile, capped for browser
// responsiveness and defaulting to 640x360 when the profile is unknown.
func snapshotSize(width, height int) (int, int) {
	if width <= 0 || height <= 0 {
		return 640, 360
	}
	if width > snapshotMaxWidth {
		return snapshotMaxWidth, snapshotMaxHeight
	}
	return width, height
}

// RenderPreview draws a synthetic preview frame: a dark background tinted
// by the PTZ state, a grid, and a crosshair offset by pan/tilt.
func RenderPreview(width, height int, pan, tilt, zoom float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	bgR := uint8(24 + int((pan+1.0)*15))
	bgG := uint8(28 + int((tilt+1.0)*15))
	bgB := uint8(40 + int(zoom*30))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{bgR, bgG, bgB, 255}}, image.Point{}, draw.Src)

	gridCol := color.RGBA{60, 70, 90, 255}
	for x := 0; x < width; x += 40 {
		for y := 0; y < height; y += 2 {
			img.Set(x, y, gridCol)
		}
	}
	for y := 0; y < height; y += 40 {
		for x := 0; x < width; x += 2 {
			img.Set(x, y, gridCol)
		}
	}

	centerX, centerY := width/2, height/2
	targetX := centerX + int(pan*float64(centerX/2))
	targetY := centerY - int(tilt*float64(centerY/2))
	crossCol := color.RGBA{0, 255, 180, 255}
	for x := targetX - 25; x <= targetX+25; x++ {
		if x >= 0 && x < width && targetY >= 0 && targetY < height {
			img.Set(x, targetY, crossCol)
		}
	}
	for y := targetY - 25; y <= targetY+25; y++ {
		if y >= 0 && y < height && targetX >= 0 && targetX < width {
			img.Set(targetX, y, crossCol)
		}
	}
	return img
}

func (h *APIHandler) renderSnapshotJPEG(token string) []byte {
	prof, _ := h.cfgMgr.GetProfile(token)
	width, height := snapshotSize(prof.Video.Resolution.Width, prof.Video.Resolution.Height)
	pan, tilt, zoom, _ := h.ptz.GetStatus()

	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, RenderPreview(width, height, pan, tilt, zoom), &jpeg.Options{Quality: 80})
	return buf.Bytes()
}

func tokenFromPath(path, prefix string) string {
	token := strings.TrimPrefix(path, prefix)
	if token == "" {
		return "Profile_1"
	}
	return token
}

func (h *APIHandler) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	data := h.renderSnapshotJPEG(tokenFromPath(r.URL.Path, "/api/snapshot/"))
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write(data)
}

func (h *APIHandler) handleMJPEG(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming not supported")
		return
	}
	token := tokenFromPath(r.URL.Path, "/api/mjpeg/")

	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "close")

	ticker := time.NewTicker(mjpegFrameDelay)
	defer ticker.Stop()
	ctx := r.Context()

	for {
		if err := writeMJPEGFrame(w, h.renderSnapshotJPEG(token)); err != nil {
			return
		}
		flusher.Flush()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// writeMJPEGFrame writes one multipart frame.
func writeMJPEGFrame(w io.Writer, data []byte) error {
	if _, err := fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\r\n")
	return err
}

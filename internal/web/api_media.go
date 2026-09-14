package web

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// syntheticFrameDelay paces the MJPEG stream while no live frame exists.
	syntheticFrameDelay = 200 * time.Millisecond
	// liveKeepaliveDelay re-sends the last live frame when the encoder stalls
	// so that browsers and VMS clients keep the connection open.
	liveKeepaliveDelay = 2 * time.Second

	// sourceHeader tells clients whether a frame came from the live encoder
	// or from the synthetic fallback renderer.
	sourceHeader = "X-MockCam-Source"
)

func tokenFromPath(path, prefix string) string {
	token := strings.TrimPrefix(path, prefix)
	if token == "" {
		return "Profile_1"
	}
	return token
}

// handleSnapshot serves GET /api/snapshot/{token} as a single JPEG — the
// same URL that ONVIF GetSnapshotUri advertises to VMS clients.
func (h *APIHandler) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	data, source := h.core.Snapshot(tokenFromPath(r.URL.Path, "/api/snapshot/"))
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set(sourceHeader, source)
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(data)
}

// handleMJPEG serves GET /api/mjpeg/{token} as a multipart/x-mixed-replace
// stream. Live frames are pushed as soon as the encoder produces them; the
// synthetic preview is streamed at 5 fps until the first live frame arrives.
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
	if _, _, live := h.core.LiveFrame(token); live {
		w.Header().Set(sourceHeader, "live")
	} else {
		w.Header().Set(sourceHeader, "synthetic")
	}

	ctx := r.Context()
	var lastSeq uint64
	for {
		frame, next, live := h.core.LiveFrame(token)

		var data []byte
		wait := syntheticFrameDelay
		switch {
		case live && frame.Seq != lastSeq:
			data = frame.JPEG
			lastSeq = frame.Seq
			wait = liveKeepaliveDelay
		case live:
			data = frame.JPEG // keepalive: repeat the last frame
			wait = liveKeepaliveDelay
		default:
			data = h.core.SyntheticSnapshot(token)
		}

		if err := writeMJPEGFrame(w, data); err != nil {
			return
		}
		flusher.Flush()

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-next: // new live frame (nil channel when no store: never fires)
			timer.Stop()
		case <-timer.C:
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

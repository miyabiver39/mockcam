package frames

import (
	"bytes"
	"sync"
)

var (
	jpegSOI = []byte{0xFF, 0xD8}
	jpegEOI = []byte{0xFF, 0xD9}
)

// maxPendingBytes bounds the reassembly buffer so a corrupt stream that
// never terminates a frame cannot grow memory without limit (8 MiB is far
// above any preview frame MockCam produces).
const maxPendingBytes = 8 << 20

// Sink receives complete frames.
type Sink interface {
	Publish(token string, jpeg []byte)
}

// Splitter is an io.Writer that reassembles a raw MJPEG byte stream (as
// produced by "ffmpeg -f mjpeg pipe:1") into individual JPEG images and
// hands each one to a Sink. It tolerates frames split across writes and
// discards garbage between frames.
type Splitter struct {
	token string
	sink  Sink

	mu      sync.Mutex
	pending []byte
	frames  uint64
}

// NewSplitter creates a Splitter publishing frames of token to sink.
func NewSplitter(token string, sink Sink) *Splitter {
	return &Splitter{token: token, sink: sink}
}

// Write implements io.Writer; it never returns an error so the producing
// process is not interrupted by a slow or absent consumer.
func (s *Splitter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pending = append(s.pending, p...)
	for {
		start := bytes.Index(s.pending, jpegSOI)
		if start < 0 {
			// No frame start in the buffer: keep only a possible trailing 0xFF.
			s.pending = keepTail(s.pending, 1)
			return len(p), nil
		}
		if start > 0 {
			s.pending = s.pending[start:] // drop garbage before the frame
		}
		end := bytes.Index(s.pending[2:], jpegEOI)
		if end < 0 {
			if len(s.pending) > maxPendingBytes {
				s.pending = s.pending[:0] // corrupt stream; resynchronise
			}
			return len(p), nil
		}
		frameLen := end + 2 + len(jpegEOI)
		frame := make([]byte, frameLen)
		copy(frame, s.pending[:frameLen])
		s.pending = s.pending[frameLen:]
		s.frames++
		if s.sink != nil {
			s.sink.Publish(s.token, frame)
		}
	}
}

// Frames returns the number of complete frames emitted so far.
func (s *Splitter) Frames() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frames
}

func keepTail(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	tail := b[len(b)-n:]
	if tail[0] != 0xFF {
		return b[:0]
	}
	return append(b[:0], tail...)
}

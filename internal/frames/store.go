// Package frames keeps the most recent JPEG frame of every stream profile.
// The FFmpeg workers push frames from a low-rate MJPEG side output; the web
// layer serves them as still snapshots and as an MJPEG stream.
package frames

import (
	"sync"
	"time"
)

// Frame is one JPEG image with bookkeeping.
type Frame struct {
	Token string
	JPEG  []byte
	Seq   uint64
	At    time.Time
}

// Info is a read-only summary used by diagnostics.
type Info struct {
	Token     string    `json:"token"`
	Frames    uint64    `json:"frames"`
	Bytes     int       `json:"last_frame_bytes"`
	LastFrame time.Time `json:"last_frame_at"`
}

type entry struct {
	frame  Frame
	notify chan struct{} // closed and replaced whenever a new frame arrives
}

// Store holds the latest frame per token and lets consumers wait for updates.
type Store struct {
	mu      sync.RWMutex
	entries map[string]*entry
	now     func() time.Time
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return &Store{entries: make(map[string]*entry), now: time.Now}
}

// Publish stores jpeg as the newest frame of token and wakes up waiters.
// The slice is retained, so callers must hand over an unshared copy.
func (s *Store) Publish(token string, jpeg []byte) {
	if len(jpeg) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[token]
	if !ok {
		e = &entry{notify: make(chan struct{})}
		s.entries[token] = e
	}
	e.frame = Frame{Token: token, JPEG: jpeg, Seq: e.frame.Seq + 1, At: s.now()}
	close(e.notify)
	e.notify = make(chan struct{})
}

// Latest returns the newest frame of token and a channel that is closed when
// a newer frame is published. ok is false when no frame has been seen yet;
// the returned channel is still valid in that case so callers can wait for
// the first frame.
func (s *Store) Latest(token string) (frame Frame, next <-chan struct{}, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, exists := s.entries[token]
	if !exists {
		e = &entry{notify: make(chan struct{})}
		s.entries[token] = e
	}
	return e.frame, e.notify, e.frame.Seq > 0
}

// Forget drops the frame of a removed profile.
func (s *Store) Forget(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[token]; ok {
		close(e.notify) // release waiters; they will see Seq unchanged and re-check
		delete(s.entries, token)
	}
}

// Infos returns a summary for every token that has received a frame.
func (s *Store) Infos() []Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Info, 0, len(s.entries))
	for token, e := range s.entries {
		if e.frame.Seq == 0 {
			continue
		}
		out = append(out, Info{Token: token, Frames: e.frame.Seq, Bytes: len(e.frame.JPEG), LastFrame: e.frame.At})
	}
	return out
}

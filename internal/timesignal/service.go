// Package timesignal generates the "117" speaking-clock audio track:
// a spoken announcement of the upcoming 10-second mark followed by 880 Hz
// preview pips and a mark tone. Audio is served as raw 16-bit PCM over HTTP
// so that the FFmpeg workers can consume it as a live input.
package timesignal

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// SampleRate of the PCM produced by the service. 48 kHz matches the
	// native rate of the HTS voice models so Open JTalk output needs no
	// resampling.
	SampleRate = 48000
	// ChunkMS is the duration of one audio packet written to the client.
	ChunkMS = 100
	// ChunkSamples is the number of samples in one packet.
	ChunkSamples = SampleRate * ChunkMS / 1000

	// voicePeak is the normalised peak level of spoken phrases; together with
	// the tone amplitudes it keeps the mix comfortably below full scale.
	voicePeak = 0.6
	// silenceThreshold is the magnitude below which leading/trailing samples
	// are considered silence when trimming TTS output.
	silenceThreshold = 250
)

// Service provides streaming speaking-clock audio.
type Service struct {
	synth Synthesizer
	now   func() time.Time

	mu    sync.Mutex
	cache map[string][]int16
}

// NewService builds a Service using the TTS engines available on this host.
func NewService() *Service {
	chain := DetectSynthesizers(ExecRunner{})
	log.Printf("[timesignal] TTS backends: %s", chain.Name())
	return NewServiceWith(chain, time.Now)
}

// NewServiceWith builds a Service with an explicit synthesizer and clock.
func NewServiceWith(synth Synthesizer, now func() time.Time) *Service {
	return &Service{
		synth: synth,
		now:   now,
		cache: make(map[string][]int16),
	}
}

// Phrase returns the PCM (at SampleRate) for the given sentence, using the
// cache when possible. Failures fall back to a chime so the stream never
// stalls.
func (s *Service) Phrase(ctx context.Context, text, lang string) []int16 {
	key := lang + "\x00" + text

	s.mu.Lock()
	if pcm, ok := s.cache[key]; ok {
		s.mu.Unlock()
		return pcm
	}
	s.mu.Unlock()

	pcm, err := s.synth.Synthesize(ctx, text, lang)
	if err != nil || len(pcm.Samples) == 0 {
		log.Printf("[timesignal] synthesis failed for %q (%s): %v; using chime", text, lang, err)
		pcm, _ = Chime{SampleRate: SampleRate}.Synthesize(ctx, text, lang)
	}

	samples := Resample(pcm, SampleRate).Samples
	samples = TrimSilence(samples, silenceThreshold, SampleRate)
	samples = Normalize(samples, voicePeak)

	s.mu.Lock()
	s.cache[key] = samples
	s.mu.Unlock()
	return samples
}

// CacheSize returns the number of cached phrases (for diagnostics/tests).
func (s *Service) CacheSize() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cache)
}

// Streamer is the per-connection state machine that turns wall-clock time
// into audio chunks. It is deterministic given the clock, which is what
// makes the audio schedule unit-testable.
type Streamer struct {
	svc  *Service
	lang string

	lastKey string
	phrase  []int16
	offset  int
}

// NewStreamer creates a streamer for lang ("ja" or "en").
func (s *Service) NewStreamer(lang string) *Streamer {
	if lang != "en" {
		lang = "ja"
	}
	return &Streamer{svc: s, lang: lang}
}

// Lang returns the language of the streamer.
func (st *Streamer) Lang() string { return st.lang }

// Speaking reports whether a phrase is currently being played out.
func (st *Streamer) Speaking() bool { return st.offset < len(st.phrase) }

// Chunk fills dst with the audio that should play starting at now.
// It starts a new announcement when now enters the announcement window of
// a not-yet-announced block, continues any phrase in progress, and mixes
// the tones on top.
func (st *Streamer) Chunk(ctx context.Context, now time.Time, dst []int16) {
	if ann, ok := AnnouncementFor(now); ok && ann.Key != st.lastKey {
		st.lastKey = ann.Key
		st.phrase = st.svc.Phrase(ctx, BuildPhrase(st.lang, ann.Hour, ann.Minute, ann.Second), st.lang)
		st.offset = 0
	}

	for i := range dst {
		dst[i] = 0
	}
	if st.offset < len(st.phrase) {
		st.offset += copy(dst, st.phrase[st.offset:])
	}

	firstSample := now.Second()*SampleRate + int(int64(now.Nanosecond())*SampleRate/1e9)
	RenderTones(dst, firstSample, SampleRate)
}

// Stream writes PCM chunks to w every ChunkMS until ctx is cancelled or the
// writer fails. flush, when non-nil, is called after every chunk.
func (st *Streamer) Stream(ctx context.Context, w io.Writer, flush func()) error {
	ticker := time.NewTicker(ChunkMS * time.Millisecond)
	defer ticker.Stop()

	pcm := make([]int16, ChunkSamples)
	buf := make([]byte, ChunkSamples*2)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			st.Chunk(ctx, st.svc.now(), pcm)
			buf = encodeLE16(pcm, buf)
			if _, err := w.Write(buf); err != nil {
				return err
			}
			if flush != nil {
				flush()
			}
		}
	}
}

// ContentType is the MIME type advertised for the PCM stream.
func ContentType() string {
	return fmt.Sprintf("audio/l16; rate=%d; channels=1", SampleRate)
}

// HandleAudioStream serves GET /api/audio/timesignal?lang=ja|en as an
// endless 16-bit little-endian mono PCM stream at SampleRate.
func (s *Service) HandleAudioStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", ContentType())
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "close")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	lang := strings.ToLower(r.URL.Query().Get("lang"))
	st := s.NewStreamer(lang)
	_ = st.Stream(r.Context(), w, flusher.Flush)
}

package timesignal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// recordingSynth returns a fixed one-second 48 kHz buffer and records phrases.
type recordingSynth struct {
	phrases []string
	fail    bool
}

func (r *recordingSynth) Name() string         { return "recording" }
func (r *recordingSynth) Supports(string) bool { return true }
func (r *recordingSynth) Synthesize(_ context.Context, text, _ string) (PCM, error) {
	r.phrases = append(r.phrases, text)
	if r.fail {
		return PCM{}, errors.New("tts down")
	}
	samples := make([]int16, SampleRate) // exactly one second
	for i := range samples {
		samples[i] = 8000
	}
	return PCM{SampleRate: SampleRate, Samples: samples}, nil
}

func TestServicePhraseCachesAndNormalizes(t *testing.T) {
	synth := &recordingSynth{}
	svc := NewServiceWith(synth, time.Now)

	a := svc.Phrase(context.Background(), "テスト", "ja")
	b := svc.Phrase(context.Background(), "テスト", "ja")
	if len(synth.phrases) != 1 {
		t.Fatalf("second call should hit the cache, synth called %d times", len(synth.phrases))
	}
	if len(a) == 0 || len(a) != len(b) {
		t.Fatal("cached phrase mismatch")
	}
	// Peak normalised to voicePeak.
	peak := int16(0)
	for _, v := range a {
		if v > peak {
			peak = v
		}
	}
	wantPeak := voicePeak * 32767
	if want := int16(wantPeak); peak < want-1 || peak > want+1 {
		t.Fatalf("peak = %d, want ~%d", peak, want)
	}
	// Different language → different cache entry.
	svc.Phrase(context.Background(), "テスト", "en")
	if svc.CacheSize() != 2 {
		t.Fatalf("cache size = %d, want 2", svc.CacheSize())
	}
}

func TestServicePhraseFallsBackToChime(t *testing.T) {
	svc := NewServiceWith(&recordingSynth{fail: true}, time.Now)
	pcm := svc.Phrase(context.Background(), "x", "ja")
	if len(pcm) == 0 {
		t.Fatal("failed synthesis should still produce audio (chime)")
	}
}

func TestStreamerSchedule(t *testing.T) {
	synth := &recordingSynth{}
	svc := NewServiceWith(synth, time.Now)
	st := svc.NewStreamer("ja")
	if st.Lang() != "ja" {
		t.Fatal("lang")
	}

	base := time.Date(2026, 9, 15, 10, 30, 0, 0, time.UTC)
	buf := make([]int16, ChunkSamples)

	// :00.0 — mark tone, no speech.
	st.Chunk(context.Background(), base, buf)
	if st.Speaking() {
		t.Fatal("no announcement should start at :00")
	}
	if rms(buf) < 0.1 {
		t.Fatal("mark tone expected at :00")
	}

	// :01.0 — announcement for :10 starts and the voice is present.
	st.Chunk(context.Background(), base.Add(1*time.Second), buf)
	if !st.Speaking() {
		t.Fatal("announcement should start at :01")
	}
	if len(synth.phrases) != 1 || synth.phrases[0] != "30分10秒をお知らせします" {
		t.Fatalf("phrases = %v", synth.phrases)
	}
	if rms(buf) < 0.1 {
		t.Fatal("voice should be audible in the :01 chunk")
	}

	// Ticks through the rest of the window must not restart the phrase.
	for ms := 100; ms < 3000; ms += 100 {
		st.Chunk(context.Background(), base.Add(1*time.Second+time.Duration(ms)*time.Millisecond), buf)
	}
	if len(synth.phrases) != 1 {
		t.Fatalf("phrase re-triggered: %v", synth.phrases)
	}
	// The one-second phrase has now been fully played out.
	if st.Speaking() {
		t.Fatal("phrase should have finished")
	}

	// :05.0 — silence (no tone, no voice).
	st.Chunk(context.Background(), base.Add(5*time.Second), buf)
	if rms(buf) != 0 {
		t.Fatalf("expected silence at :05, rms=%v", rms(buf))
	}

	// :11.0 — next block announces :20.
	st.Chunk(context.Background(), base.Add(11*time.Second), buf)
	if len(synth.phrases) != 2 || synth.phrases[1] != "30分20秒をお知らせします" {
		t.Fatalf("phrases = %v", synth.phrases)
	}

	// :51.0 — announces the full time on the minute.
	st.Chunk(context.Background(), base.Add(51*time.Second), buf)
	if last := synth.phrases[len(synth.phrases)-1]; last != "午前10時31分をお知らせします" {
		t.Fatalf("minute announcement = %q", last)
	}
}

func TestStreamerLateJoinSkipsBlock(t *testing.T) {
	synth := &recordingSynth{}
	svc := NewServiceWith(synth, time.Now)
	st := svc.NewStreamer("en")

	// Joining at :06 must not speak (it would collide with the pips).
	base := time.Date(2026, 9, 15, 10, 30, 6, 0, time.UTC)
	buf := make([]int16, ChunkSamples)
	st.Chunk(context.Background(), base, buf)
	if len(synth.phrases) != 0 {
		t.Fatalf("late join should stay quiet, got %v", synth.phrases)
	}
	// Unknown language normalises to ja.
	if svc.NewStreamer("de").Lang() != "ja" {
		t.Fatal("unknown lang should default to ja")
	}
}

func TestStreamerStreamWritesChunksUntilCancelled(t *testing.T) {
	svc := NewServiceWith(&recordingSynth{}, func() time.Time {
		return time.Date(2026, 9, 15, 10, 30, 5, 0, time.UTC)
	})
	st := svc.NewStreamer("ja")

	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	flushes := 0
	err := st.Stream(ctx, &out, func() { flushes++ })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if out.Len() < 2*ChunkSamples*2 || out.Len()%(ChunkSamples*2) != 0 {
		t.Fatalf("wrote %d bytes, want a multiple of %d and at least two chunks", out.Len(), ChunkSamples*2)
	}
	if flushes == 0 {
		t.Fatal("flush callback not invoked")
	}

	// Writer failure ends the stream.
	err = st.Stream(context.Background(), failingWriter{}, nil)
	if err == nil || err.Error() != "closed" {
		t.Fatalf("writer error should propagate, got %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestHandleAudioStream(t *testing.T) {
	svc := NewServiceWith(&recordingSynth{}, time.Now)
	srv := httptest.NewServer(http.HandlerFunc(svc.HandleAudioStream))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/audio/timesignal?lang=en", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "audio/l16") || !strings.Contains(ct, "rate=48000") {
		t.Fatalf("content-type = %q", ct)
	}
	data, _ := io.ReadAll(resp.Body) // ends when ctx expires
	if len(data) < ChunkSamples*2 {
		t.Fatalf("received only %d bytes", len(data))
	}

	// Non-GET is rejected.
	postResp, err := http.Post(srv.URL, "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", postResp.StatusCode)
	}
}

func TestContentType(t *testing.T) {
	if ContentType() != "audio/l16; rate=48000; channels=1" {
		t.Fatalf("got %q", ContentType())
	}
}

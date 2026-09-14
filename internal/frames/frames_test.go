package frames

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

// fakeJPEG builds a minimal SOI ... EOI byte sequence with the given payload.
func fakeJPEG(payload string) []byte {
	return append(append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, []byte(payload)...), 0xFF, 0xD9)
}

type recordingSink struct {
	mu     sync.Mutex
	frames [][]byte
	tokens []string
}

func (r *recordingSink) Publish(token string, jpeg []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, jpeg)
	r.tokens = append(r.tokens, token)
}

func TestSplitterReassemblesAcrossWrites(t *testing.T) {
	sink := &recordingSink{}
	sp := NewSplitter("Profile_1", sink)

	a, b := fakeJPEG("first"), fakeJPEG("second")
	stream := append(append([]byte("garbage"), a...), b...)

	// Feed one byte at a time to exercise every split point.
	for i := range stream {
		if n, err := sp.Write(stream[i : i+1]); err != nil || n != 1 {
			t.Fatalf("write %d: n=%d err=%v", i, n, err)
		}
	}
	if len(sink.frames) != 2 || !bytes.Equal(sink.frames[0], a) || !bytes.Equal(sink.frames[1], b) {
		t.Fatalf("frames = %d %q", len(sink.frames), sink.frames)
	}
	if sink.tokens[0] != "Profile_1" || sp.Frames() != 2 {
		t.Fatal("token / counter")
	}

	// A frame delivered in one write and a partial tail.
	c := fakeJPEG("third")
	_, _ = sp.Write(append(c, 0xFF, 0xD8, 0x01))
	if len(sink.frames) != 3 || !bytes.Equal(sink.frames[2], c) {
		t.Fatalf("third frame missing: %d", len(sink.frames))
	}
	_, _ = sp.Write([]byte{0x02, 0xFF, 0xD9})
	if len(sink.frames) != 4 || !bytes.Equal(sink.frames[3], []byte{0xFF, 0xD8, 0x01, 0x02, 0xFF, 0xD9}) {
		t.Fatalf("split tail frame: %q", sink.frames)
	}

	// Published frames must not alias the internal buffer.
	sink.frames[0][2] = 0x00
	_, _ = sp.Write(fakeJPEG("fourth"))
	if sink.frames[4][2] != 0xFF {
		t.Fatal("frame buffers must be independent copies")
	}
}

func TestSplitterDropsGarbageAndBoundsBuffer(t *testing.T) {
	sp := NewSplitter("x", nil) // nil sink must be safe
	_, _ = sp.Write(bytes.Repeat([]byte{0x00}, 4096))
	if len(sp.pending) != 0 {
		t.Fatalf("garbage should be discarded, pending=%d", len(sp.pending))
	}
	// A trailing 0xFF is kept because it may start an SOI marker.
	_, _ = sp.Write([]byte{0x00, 0xFF})
	if len(sp.pending) != 1 || sp.pending[0] != 0xFF {
		t.Fatalf("pending = %v", sp.pending)
	}
	_, _ = sp.Write([]byte{0xD8, 0xAA, 0xFF, 0xD9})
	if sp.Frames() != 1 {
		t.Fatal("marker split across the 0xFF boundary should still be detected")
	}

	// An unterminated frame larger than the cap is dropped.
	_, _ = sp.Write([]byte{0xFF, 0xD8})
	_, _ = sp.Write(bytes.Repeat([]byte{0x11}, maxPendingBytes+10))
	if len(sp.pending) != 0 {
		t.Fatalf("oversized pending frame should be reset, got %d", len(sp.pending))
	}
}

func TestStorePublishLatestAndNotify(t *testing.T) {
	s := NewStore()
	fixed := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixed }

	frame, next, ok := s.Latest("Profile_1")
	if ok || frame.Seq != 0 {
		t.Fatal("no frame yet")
	}
	select {
	case <-next:
		t.Fatal("notify channel must not be closed before a publish")
	default:
	}

	s.Publish("Profile_1", nil) // ignored
	if _, _, ok := s.Latest("Profile_1"); ok {
		t.Fatal("empty frames are ignored")
	}

	s.Publish("Profile_1", fakeJPEG("a"))
	select {
	case <-next:
	default:
		t.Fatal("publish should close the previous notify channel")
	}
	frame, next2, ok := s.Latest("Profile_1")
	if !ok || frame.Seq != 1 || frame.At != fixed || frame.Token != "Profile_1" || !bytes.Equal(frame.JPEG, fakeJPEG("a")) {
		t.Fatalf("frame = %+v", frame)
	}
	s.Publish("Profile_1", fakeJPEG("b"))
	<-next2
	if frame, _, _ := s.Latest("Profile_1"); frame.Seq != 2 {
		t.Fatal("seq should increment")
	}

	infos := s.Infos()
	if len(infos) != 1 || infos[0].Frames != 2 || infos[0].Token != "Profile_1" {
		t.Fatalf("infos = %+v", infos)
	}

	_, next3, _ := s.Latest("Profile_1")
	s.Forget("Profile_1")
	<-next3 // waiters are released
	if _, _, ok := s.Latest("Profile_1"); ok {
		t.Fatal("forgotten token should have no frame")
	}
	s.Forget("unknown") // no-op
}

func TestStoreConcurrentPublishers(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.Publish("P", fakeJPEG("x"))
				_, _, _ = s.Latest("P")
			}
		}()
	}
	wg.Wait()
	if frame, _, _ := s.Latest("P"); frame.Seq != 800 {
		t.Fatalf("seq = %d", frame.Seq)
	}
}

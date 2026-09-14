package logger

import (
	"encoding/json"
	"io"
	"log"
	"sync"
	"testing"
)

func init() {
	// Silence the stdout mirror during tests.
	log.SetOutput(io.Discard)
}

func TestRingBufferKeepsCapacity(t *testing.T) {
	r := NewRingLogger(3)
	for i := 0; i < 5; i++ {
		r.Info("t", "msg %d", i)
	}
	got := r.GetRecentLogs(0)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Message != "msg 2" || got[2].Message != "msg 4" {
		t.Fatalf("oldest entries should be evicted: %+v", got)
	}
	if got := r.GetRecentLogs(2); len(got) != 2 || got[0].Message != "msg 3" {
		t.Fatalf("limit: %+v", got)
	}
	if got := r.GetRecentLogs(100); len(got) != 3 {
		t.Fatalf("limit larger than buffer: %d", len(got))
	}
	if NewRingLogger(0).capacity != 1000 {
		t.Fatal("non-positive capacity should fall back to 1000")
	}
}

func TestLevelFiltering(t *testing.T) {
	r := NewRingLogger(10)
	if r.GetMinLevel() != LevelInfo {
		t.Fatal("default level should be INFO")
	}
	r.Debug("t", "hidden")
	r.Info("t", "shown")
	if got := r.GetRecentLogs(0); len(got) != 1 || got[0].Level != LevelInfo {
		t.Fatalf("debug should be filtered at INFO: %+v", got)
	}

	r.SetMinLevel(LevelError)
	r.Warn("t", "hidden")
	r.Error("t", "boom")
	got := r.GetRecentLogs(0)
	if len(got) != 2 || got[1].Level != LevelError || got[1].Message != "boom" {
		t.Fatalf("warn should be filtered at ERROR: %+v", got)
	}

	r.SetMinLevel(LevelDebug)
	r.Debug("t", "now visible")
	if got := r.GetRecentLogs(1); got[0].Message != "now visible" {
		t.Fatal("debug should pass at DEBUG")
	}

	// Unknown levels are treated as INFO severity.
	if levelSeverity("WHATEVER") != levelSeverity(LevelInfo) {
		t.Fatal("unknown level severity")
	}
}

func TestSubscribeUnsubscribe(t *testing.T) {
	r := NewRingLogger(10)
	var mu sync.Mutex
	var received []LogEntry
	id := r.Subscribe(func(e LogEntry) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	})

	r.Info("src", "one")
	r.Unsubscribe(id)
	r.Info("src", "two")

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 || received[0].Message != "one" || received[0].Source != "src" {
		t.Fatalf("received = %+v", received)
	}
	if received[0].Timestamp == "" {
		t.Fatal("timestamp missing")
	}
}

func TestSubscriberMayLogWithoutDeadlock(t *testing.T) {
	r := NewRingLogger(10)
	done := make(chan struct{})
	r.Subscribe(func(e LogEntry) {
		if e.Message == "first" {
			r.Warn("nested", "from subscriber") // must not deadlock
			close(done)
		}
	})
	r.Info("t", "first")
	<-done
	if got := r.GetRecentLogs(0); len(got) != 2 {
		t.Fatalf("nested log missing: %+v", got)
	}
}

func TestExportJSON(t *testing.T) {
	r := NewRingLogger(10)
	r.Info("a", "x")
	data, err := r.ExportJSON()
	if err != nil {
		t.Fatal(err)
	}
	var entries []LogEntry
	if err := json.Unmarshal(data, &entries); err != nil || len(entries) != 1 {
		t.Fatalf("export: err=%v entries=%v", err, entries)
	}
}

func TestPackageLevelAdapters(t *testing.T) {
	old := GlobalLogger
	GlobalLogger = NewRingLogger(10)
	t.Cleanup(func() { GlobalLogger = old })

	Debugf("s", "d")
	Infof("s", "i")
	Warnf("s", "w")
	Errorf("s", "e")
	got := GlobalLogger.GetRecentLogs(0)
	if len(got) != 3 { // debug filtered at default INFO
		t.Fatalf("adapters: %+v", got)
	}
}

func TestConcurrentLogging(t *testing.T) {
	r := NewRingLogger(50)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				r.Info("c", "%d-%d", n, j)
			}
		}(i)
	}
	wg.Wait()
	if len(r.GetRecentLogs(0)) != 50 {
		t.Fatal("buffer should be full")
	}
}

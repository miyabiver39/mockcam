package rtsp

import (
	"path/filepath"
	"testing"
	"time"

	"mockcam/internal/config"
)

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"/live/Profile_1": "Profile_1",
		"live/Profile_1":  "Profile_1",
		"/Profile_2":      "Profile_2",
		"Profile_3":       "Profile_3",
		"/live/":          "",
		"":                "",
	}
	for in, want := range cases {
		if got := normalizePath(in); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:5000":   true,
		"127.0.0.1":        true,
		"[::1]:5000":       true,
		"::1":              true,
		"192.168.1.10:554": false,
		"10.0.0.1":         false,
		"not-an-ip":        false,
		"":                 false,
	}
	for in, want := range cases {
		if got := isLoopback(in); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRemoteHostOf(t *testing.T) {
	if got := remoteHostOf("192.168.0.5:61000"); got != "192.168.0.5" {
		t.Fatalf("got %q", got)
	}
	if got := remoteHostOf("[fe80::1]:554"); got != "fe80::1" {
		t.Fatalf("got %q", got)
	}
	if got := remoteHostOf("plainhost"); got != "plainhost" {
		t.Fatalf("got %q", got)
	}
}

func TestThroughputMeter(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := newThroughputMeter(start)

	// 1000 packets of 1000 bytes = 8,000,000 bits.
	for i := 0; i < 1000; i++ {
		m.Record(1000)
	}

	// Less than one second later the bitrate is not recomputed yet.
	packets, bytes, kbps := m.Snapshot(start.Add(500 * time.Millisecond))
	if packets != 1000 || bytes != 1_000_000 {
		t.Fatalf("counters = (%d,%d), want (1000,1000000)", packets, bytes)
	}
	if kbps != 0 {
		t.Fatalf("bitrate should not be computed before 1s elapsed, got %v", kbps)
	}

	// Exactly two seconds later: 8,000,000 bits / 2 s = 4,000 kbps.
	_, _, kbps = m.Snapshot(start.Add(2 * time.Second))
	if kbps != 4000 {
		t.Fatalf("bitrate = %v kbps, want 4000", kbps)
	}

	// No new traffic: the next window reports 0 kbps.
	_, _, kbps = m.Snapshot(start.Add(4 * time.Second))
	if kbps != 0 {
		t.Fatalf("bitrate after idle window = %v, want 0", kbps)
	}
}

func TestServerStatsAndClientsWithoutNetwork(t *testing.T) {
	cfgMgr, err := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(cfgMgr, newFakeValidator("admin", "admin1234"))

	fixed := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixed }

	if got := s.GetClientCount(); got != 0 {
		t.Fatalf("initial client count = %d", got)
	}
	if got := s.GetClients(); len(got) != 0 {
		t.Fatalf("initial clients = %v", got)
	}
	if got := s.StreamTokens(); len(got) != 0 {
		t.Fatalf("initial streams = %v", got)
	}

	stats := s.GetStats()
	if stats.PacketsSent != 0 || stats.BytesSent != 0 || stats.ActiveReaders != 0 {
		t.Fatalf("unexpected initial stats: %+v", stats)
	}

	// Simulate a reader record and verify the duration is derived from the clock.
	s.mu.Lock()
	s.readers[nil] = &clientSessionRecord{id: "sess-1", remoteIP: "10.0.0.9", path: "Profile_1", startTime: fixed.Add(-42 * time.Second)}
	s.mu.Unlock()

	clients := s.GetClients()
	if len(clients) != 1 || clients[0].Duration != 42 || clients[0].RemoteIP != "10.0.0.9" || clients[0].Path != "Profile_1" {
		t.Fatalf("unexpected clients: %+v", clients)
	}
	if s.GetStats().ActiveReaders != 1 {
		t.Fatal("active readers should reflect the reader map")
	}

	// Closing without Start must be safe.
	s.Close()
}

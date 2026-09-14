package rtsp

import (
	"sync"
	"sync/atomic"
	"time"
)

// ClientInfo represents an active RTSP consumer.
type ClientInfo struct {
	ID        string `json:"id"`
	RemoteIP  string `json:"remote_ip"`
	Path      string `json:"path"`
	Transport string `json:"transport"`
	Duration  int64  `json:"duration_seconds"`
}

// StreamStats tracks packet and bandwidth statistics across all profiles.
type StreamStats struct {
	PacketsSent   int64   `json:"packets_sent"`
	BytesSent     int64   `json:"bytes_sent"`
	BitrateKbps   float64 `json:"bitrate_kbps"`
	ActiveReaders int     `json:"active_readers"`
}

// throughputMeter accumulates packet/byte counters and derives a bitrate
// from the delta observed between two Snapshot calls at least one second apart.
type throughputMeter struct {
	packets atomic.Int64
	bytes   atomic.Int64

	mu       sync.Mutex
	lastAt   time.Time
	lastByte int64
	kbps     float64
}

func newThroughputMeter(now time.Time) *throughputMeter {
	return &throughputMeter{lastAt: now}
}

// Record adds one RTP packet of the given payload size.
func (m *throughputMeter) Record(payloadBytes int) {
	m.packets.Add(1)
	m.bytes.Add(int64(payloadBytes))
}

// Snapshot returns the cumulative counters and the most recent bitrate estimate.
// The bitrate is recomputed only when at least one second elapsed since the
// previous computation so that frequent polling does not produce noisy values.
func (m *throughputMeter) Snapshot(now time.Time) (packets, bytes int64, kbps float64) {
	packets = m.packets.Load()
	bytes = m.bytes.Load()

	m.mu.Lock()
	defer m.mu.Unlock()
	if elapsed := now.Sub(m.lastAt).Seconds(); elapsed >= 1.0 {
		m.kbps = float64((bytes-m.lastByte)*8) / elapsed / 1000.0
		m.lastByte = bytes
		m.lastAt = now
	}
	return packets, bytes, m.kbps
}

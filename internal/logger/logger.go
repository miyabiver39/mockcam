package logger

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// LogLevel represents logging severity.
type LogLevel string

const (
	LevelDebug LogLevel = "DEBUG"
	LevelInfo  LogLevel = "INFO"
	LevelWarn  LogLevel = "WARN"
	LevelError LogLevel = "ERROR"
)

// LogEntry is a structured log message.
type LogEntry struct {
	Timestamp string   `json:"timestamp"`
	Level     LogLevel `json:"level"`
	Source    string   `json:"source"`
	Message   string   `json:"message"`
}

// Broadcaster is a callback function for newly created log entries.
type Broadcaster func(entry LogEntry)

// RingLogger provides an in-memory ring buffer of recent logs and broadcasts to subscribers.
type RingLogger struct {
	mu           sync.RWMutex
	capacity     int
	entries      []LogEntry
	broadcasters map[int]Broadcaster
	nextID       int
	minLevel     LogLevel
}

var GlobalLogger = NewRingLogger(1000)

// NewRingLogger creates a new ring logger.
func NewRingLogger(capacity int) *RingLogger {
	if capacity <= 0 {
		capacity = 1000
	}
	return &RingLogger{
		capacity:     capacity,
		entries:      make([]LogEntry, 0, capacity),
		broadcasters: make(map[int]Broadcaster),
		minLevel:     LevelInfo,
	}
}

// SetMinLevel sets the minimum logging severity.
func (r *RingLogger) SetMinLevel(level LogLevel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.minLevel = level
}

// GetMinLevel returns the current minimum logging severity.
func (r *RingLogger) GetMinLevel() LogLevel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.minLevel
}

func levelSeverity(lvl LogLevel) int {
	switch lvl {
	case LevelDebug:
		return 1
	case LevelInfo:
		return 2
	case LevelWarn:
		return 3
	case LevelError:
		return 4
	default:
		return 2
	}
}

// Log records a structured message.
func (r *RingLogger) Log(level LogLevel, source, formatStr string, args ...interface{}) {
	msg := fmt.Sprintf(formatStr, args...)
	entry := LogEntry{
		Timestamp: time.Now().Format("2006-01-02 15:04:05.000"),
		Level:     level,
		Source:    source,
		Message:   msg,
	}

	// Always print to stdout
	log.Printf("[%s] [%s] %s", level, source, msg)

	r.mu.Lock()
	if len(r.entries) >= r.capacity {
		r.entries = r.entries[1:]
	}
	r.entries = append(r.entries, entry)

	// Copy broadcasters
	bcasts := make([]Broadcaster, 0, len(r.broadcasters))
	for _, b := range r.broadcasters {
		bcasts = append(bcasts, b)
	}
	r.mu.Unlock()

	// Notify broadcasters outside lock
	for _, b := range bcasts {
		b(entry)
	}
}

// Debug logs a debug message.
func (r *RingLogger) Debug(source, formatStr string, args ...interface{}) {
	if levelSeverity(LevelDebug) >= levelSeverity(r.GetMinLevel()) {
		r.Log(LevelDebug, source, formatStr, args...)
	}
}

// Info logs an informational message.
func (r *RingLogger) Info(source, formatStr string, args ...interface{}) {
	if levelSeverity(LevelInfo) >= levelSeverity(r.GetMinLevel()) {
		r.Log(LevelInfo, source, formatStr, args...)
	}
}

// Warn logs a warning message.
func (r *RingLogger) Warn(source, formatStr string, args ...interface{}) {
	if levelSeverity(LevelWarn) >= levelSeverity(r.GetMinLevel()) {
		r.Log(LevelWarn, source, formatStr, args...)
	}
}

// Error logs an error message.
func (r *RingLogger) Error(source, formatStr string, args ...interface{}) {
	r.Log(LevelError, source, formatStr, args...)
}

// Subscribe attaches a listener for real-time log streaming.
func (r *RingLogger) Subscribe(b Broadcaster) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.nextID
	r.nextID++
	r.broadcasters[id] = b
	return id
}

// Unsubscribe detaches a listener.
func (r *RingLogger) Unsubscribe(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.broadcasters, id)
}

// GetRecentLogs returns up to limit recent log entries.
func (r *RingLogger) GetRecentLogs(limit int) []LogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if limit <= 0 || limit > len(r.entries) {
		limit = len(r.entries)
	}

	start := len(r.entries) - limit
	result := make([]LogEntry, limit)
	copy(result, r.entries[start:])
	return result
}

// ExportJSON exports all currently buffered logs as JSON.
func (r *RingLogger) ExportJSON() ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return json.MarshalIndent(r.entries, "", "  ")
}

// Standard log adapters
func Debugf(source, formatStr string, args ...interface{}) {
	GlobalLogger.Debug(source, formatStr, args...)
}

func Infof(source, formatStr string, args ...interface{}) {
	GlobalLogger.Info(source, formatStr, args...)
}

func Warnf(source, formatStr string, args ...interface{}) {
	GlobalLogger.Warn(source, formatStr, args...)
}

func Errorf(source, formatStr string, args ...interface{}) {
	GlobalLogger.Error(source, formatStr, args...)
}

// Fatalf logs error and exits
func Fatalf(source, formatStr string, args ...interface{}) {
	GlobalLogger.Error(source, formatStr, args...)
	os.Exit(1)
}

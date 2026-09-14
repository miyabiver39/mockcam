package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Manager manages reading, validating, updating, and persisting MockCam configuration.
type Manager struct {
	mu   sync.RWMutex
	path string
	cfg  *Config
}

// GetDefaultPath returns the configuration file path.
// It checks CONFIG_PATH env var first. If unset, it defaults to /config/settings.json.
// On Windows, if /config is unavailable, it gracefully defaults to ./config/settings.json.
func GetDefaultPath() string {
	if envPath := os.Getenv("CONFIG_PATH"); envPath != "" {
		return envPath
	}
	defaultPath := "/config/settings.json"
	// Check if running in an environment where /config is not writable or is windows root
	if os.PathSeparator == '\\' {
		// On Windows, if not explicitly specified, default to ./config/settings.json
		return filepath.Join(".", "config", "settings.json")
	}
	return defaultPath
}

// NewManager loads configuration from path, creating default configuration if absent.
func NewManager(path string) (*Manager, error) {
	if path == "" {
		path = GetDefaultPath()
	}

	m := &Manager{
		path: path,
	}

	if err := m.loadOrInit(); err != nil {
		return nil, err
	}

	return m, nil
}

func (m *Manager) loadOrInit() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory %s: %w", dir, err)
	}

	info, err := os.Stat(m.path)
	if os.IsNotExist(err) || (err == nil && info.Size() == 0) {
		log.Printf("[config] Configuration file %s not found or empty; creating default configuration...", m.path)
		defaultCfg := DefaultConfig()
		data, err := json.MarshalIndent(defaultCfg, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal default config: %w", err)
		}

		if err := os.WriteFile(m.path, data, 0644); err != nil {
			return fmt.Errorf("failed to write default config to %s: %w", m.path, err)
		}
		m.cfg = defaultCfg
		return nil
	}

	// File exists: read and parse
	data, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("failed to read config file %s: %w", m.path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse config JSON from %s: %w", m.path, err)
	}

	m.cfg = &cfg
	return nil
}

// Get returns a deep copy of the current configuration.
func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, _ := json.Marshal(m.cfg)
	var copyCfg Config
	_ = json.Unmarshal(data, &copyCfg)
	return copyCfg
}

// GetProfile returns a copy of the profile with the given token.
func (m *Manager) GetProfile(token string) (ProfileConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, p := range m.cfg.Profiles {
		if p.Token == token {
			return p, true
		}
	}
	return ProfileConfig{}, false
}

// UpdateProfile updates or replaces a profile and saves the updated configuration.
func (m *Manager) UpdateProfile(token string, updated ProfileConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	found := false
	for i, p := range m.cfg.Profiles {
		if p.Token == token {
			updated.Token = token // keep token consistent
			m.cfg.Profiles[i] = updated
			found = true
			break
		}
	}

	if !found {
		return errors.New("profile not found: " + token)
	}

	return m.saveLocked()
}

// UpdatePTZ updates the stored pan, tilt, and zoom values.
func (m *Manager) UpdatePTZ(pan, tilt, zoom float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cfg.PTZ.Pan = pan
	m.cfg.PTZ.Tilt = tilt
	m.cfg.PTZ.Zoom = zoom
	return m.saveLocked()
}

// UpdateServerConfig updates the server and authentication configuration.
func (m *Manager) UpdateServerConfig(srv ServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cfg.Server = srv
	return m.saveLocked()
}

// Save persists the current configuration to disk.
func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked()
}

func (m *Manager) saveLocked() error {
	data, err := json.MarshalIndent(m.cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(m.path, data, 0644); err != nil {
		return fmt.Errorf("failed to save config to %s: %w", m.path, err)
	}

	return nil
}

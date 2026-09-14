package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	m, err := NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	return m, path
}

func readFile(t *testing.T, path string) Config {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestGetReturnsDeepCopy(t *testing.T) {
	m, _ := newTestManager(t)
	cfg := m.Get()
	cfg.Profiles[0].Token = "MUTATED"
	cfg.Server.RTSPPort = 1
	if again := m.Get(); again.Profiles[0].Token != "Profile_1" || again.Server.RTSPPort != 8554 {
		t.Fatal("Get must return a copy that does not alias internal state")
	}
	if p, _ := m.GetProfile("Profile_1"); p.Token != "Profile_1" {
		t.Fatal("GetProfile should be unaffected by mutation of a copy")
	}
}

func TestGetProfileMissing(t *testing.T) {
	m, _ := newTestManager(t)
	if _, ok := m.GetProfile("nope"); ok {
		t.Fatal("missing profile should report ok=false")
	}
}

func TestUpdateProfileKeepsTokenAndPersists(t *testing.T) {
	m, path := newTestManager(t)
	p, _ := m.GetProfile("Profile_2")
	p.Token = "renamed" // must be ignored
	p.Name = "New name"
	if err := m.UpdateProfile("Profile_2", p); err != nil {
		t.Fatal(err)
	}
	got, ok := m.GetProfile("Profile_2")
	if !ok || got.Name != "New name" {
		t.Fatalf("update not applied: %+v ok=%v", got, ok)
	}
	if readFile(t, path).Profiles[1].Name != "New name" {
		t.Fatal("update not persisted")
	}
	if err := m.UpdateProfile("missing", p); err == nil {
		t.Fatal("updating a missing profile should fail")
	}
}

func TestAddAndDeleteProfile(t *testing.T) {
	m, path := newTestManager(t)

	if err := m.AddProfile(ProfileConfig{Token: "Profile_1"}); err == nil {
		t.Fatal("duplicate token should be rejected")
	}
	if err := m.AddProfile(ProfileConfig{Token: "Profile_3", Name: "third"}); err != nil {
		t.Fatal(err)
	}
	if len(m.Get().Profiles) != 3 || len(readFile(t, path).Profiles) != 3 {
		t.Fatal("profile not added/persisted")
	}

	if err := m.DeleteProfile("nope"); err == nil {
		t.Fatal("deleting a missing profile should fail")
	}
	if err := m.DeleteProfile("Profile_3"); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteProfile("Profile_2"); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteProfile("Profile_1"); err == nil {
		t.Fatal("the last profile must not be deletable")
	}
	if got := m.Get().Profiles; len(got) != 1 || got[0].Token != "Profile_1" {
		t.Fatalf("profiles after deletes = %+v", got)
	}
}

func TestServerConfigPresetsAndReset(t *testing.T) {
	m, path := newTestManager(t)

	srv := m.Get().Server
	srv.AuthType = "none"
	srv.HTTPPort = 9090
	if err := m.UpdateServerConfig(srv); err != nil {
		t.Fatal(err)
	}
	if readFile(t, path).Server.HTTPPort != 9090 {
		t.Fatal("server config not persisted")
	}

	presets := []PTZPreset{{Name: "home", Pan: 0.1, Tilt: 0.2, Zoom: 0.3}}
	if err := m.UpdatePTZPresets(presets); err != nil {
		t.Fatal(err)
	}
	if got := m.Get().PTZ.Presets; len(got) != 1 || got[0].Name != "home" {
		t.Fatalf("presets = %+v", got)
	}

	if err := m.ResetToDefaults(); err != nil {
		t.Fatal(err)
	}
	if m.Get().Server.HTTPPort != 8080 || len(m.Get().PTZ.Presets) != 0 {
		t.Fatal("reset did not restore defaults")
	}
	if readFile(t, path).Server.HTTPPort != 8080 {
		t.Fatal("reset not persisted")
	}
}

func TestLoadExistingFileSyncsFirmwareVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	cfg := DefaultConfig()
	cfg.Server.DeviceInfo.FirmwareVersion = "0.0.1"
	cfg.Server.HTTPPort = 18080
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	got := m.Get()
	if got.Server.HTTPPort != 18080 {
		t.Fatal("existing values must be preserved")
	}
	if got.Server.DeviceInfo.FirmwareVersion != AppVersion {
		t.Fatalf("firmware version should be synced to %s, got %s", AppVersion, got.Server.DeviceInfo.FirmwareVersion)
	}
	if readFile(t, path).Server.DeviceInfo.FirmwareVersion != AppVersion {
		t.Fatal("synced version should be written back")
	}
}

func TestLoadEmptyAndInvalidFile(t *testing.T) {
	dir := t.TempDir()

	empty := filepath.Join(dir, "empty.json")
	_ = os.WriteFile(empty, nil, 0o644)
	if m, err := NewManager(empty); err != nil || len(m.Get().Profiles) != 2 {
		t.Fatalf("empty file should be replaced with defaults: err=%v", err)
	}

	broken := filepath.Join(dir, "broken.json")
	_ = os.WriteFile(broken, []byte("{not json"), 0o644)
	if _, err := NewManager(broken); err == nil {
		t.Fatal("invalid JSON must be reported, not silently overwritten")
	}
}

func TestGetDefaultPathHonoursEnv(t *testing.T) {
	t.Setenv("CONFIG_PATH", "/tmp/custom.json")
	if got := GetDefaultPath(); got != "/tmp/custom.json" {
		t.Fatalf("CONFIG_PATH ignored: %s", got)
	}
	t.Setenv("CONFIG_PATH", "")
	if got := GetDefaultPath(); got == "" {
		t.Fatal("default path should not be empty")
	}
}

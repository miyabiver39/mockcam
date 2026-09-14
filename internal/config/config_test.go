package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigInitAndSave(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "subfolder", "settings.json")

	// 1. Check auto-generation when file does not exist
	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("failed to create config manager: %v", err)
	}

	cfg := mgr.Get()
	if cfg.Server.RTSPPort != 8554 {
		t.Errorf("expected RTSP port 8554, got %d", cfg.Server.RTSPPort)
	}
	if len(cfg.Profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(cfg.Profiles))
	}
	if cfg.Profiles[0].Token != "Profile_1" {
		t.Errorf("expected Profile_1, got %s", cfg.Profiles[0].Token)
	}

	// Verify file was written to disk
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("expected config file to be created on disk")
	}

	// 2. Test profile update
	prof1, ok := mgr.GetProfile("Profile_1")
	if !ok {
		t.Fatal("expected Profile_1 to exist")
	}
	prof1.Video.Framerate = 60
	prof1.Video.BitrateLimitKbps = 6000

	if err := mgr.UpdateProfile("Profile_1", prof1); err != nil {
		t.Fatalf("failed to update profile: %v", err)
	}

	// 3. Reload from disk with a new manager to ensure persistence
	mgr2, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("failed to load saved config: %v", err)
	}

	reloadedProf1, ok := mgr2.GetProfile("Profile_1")
	if !ok {
		t.Fatal("expected reloaded Profile_1 to exist")
	}
	if reloadedProf1.Video.Framerate != 60 {
		t.Errorf("expected framerate 60, got %d", reloadedProf1.Video.Framerate)
	}
	if reloadedProf1.Video.BitrateLimitKbps != 6000 {
		t.Errorf("expected bitrate 6000, got %d", reloadedProf1.Video.BitrateLimitKbps)
	}

	// 4. Test PTZ update
	if err := mgr2.UpdatePTZ(0.5, -0.5, 0.8); err != nil {
		t.Fatalf("failed to update PTZ: %v", err)
	}
	cfgUpdated := mgr2.Get()
	if cfgUpdated.PTZ.Pan != 0.5 || cfgUpdated.PTZ.Tilt != -0.5 || cfgUpdated.PTZ.Zoom != 0.8 {
		t.Errorf("PTZ update not reflected: %+v", cfgUpdated.PTZ)
	}
}

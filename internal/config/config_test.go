package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := []byte(`
device_id: macbook
local_dir: ./notes
couchdb_url: http://localhost:5984
database: orgsync
poll_interval: 10s
include_exts:
  - .org
ignore:
  - "*.tmp"
`)
	if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeviceID != "macbook" {
		t.Fatalf("DeviceID = %q", cfg.DeviceID)
	}
	if cfg.PollInterval != 10*time.Second {
		t.Fatalf("PollInterval = %s", cfg.PollInterval)
	}
	if got := cfg.IncludeExts[0]; got != ".org" {
		t.Fatalf("IncludeExts[0] = %q", got)
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := []byte(`
device_id: macbook
local_dir: ./notes
couchdb_url: http://localhost:5984
database: orgsync
`)
	if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollInterval != 5*time.Second {
		t.Fatalf("PollInterval = %s", cfg.PollInterval)
	}
	if len(cfg.IncludeExts) != 3 {
		t.Fatalf("IncludeExts len = %d", len(cfg.IncludeExts))
	}
}

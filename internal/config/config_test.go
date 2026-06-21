package config

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	t.Setenv("DEVICE_ID", "macbook")
	t.Setenv("LOCAL_DIR", "./notes")
	t.Setenv("COUCHDB_URL", "http://localhost:5984")
	t.Setenv("COUCHDB_DATABASE", "orgsync-test")
	t.Setenv("COUCHDB_USER", "admin")
	t.Setenv("COUCHDB_PASSWORD", "password")
	t.Setenv("POLL_INTERVAL", "10s")
	t.Setenv("DRY_RUN", "true")
	t.Setenv("INCLUDE_EXTS", ".org, .md")
	t.Setenv("IGNORE", ".git,*.tmp")
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeviceID != "macbook" || cfg.Database != "orgsync-test" {
		t.Fatalf("unexpected identity config: %+v", cfg)
	}
	if cfg.LocalDir != filepath.Clean(mustAbs(t, "./notes")) {
		t.Fatalf("LocalDir = %q", cfg.LocalDir)
	}
	if cfg.PollInterval != 10*time.Second || !cfg.DryRun {
		t.Fatalf("unexpected runtime config: %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.IncludeExts, []string{".org", ".md"}) {
		t.Fatalf("IncludeExts = %#v", cfg.IncludeExts)
	}
	if !reflect.DeepEqual(cfg.Ignore, []string{".git", "*.tmp"}) {
		t.Fatalf("Ignore = %#v", cfg.Ignore)
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DEVICE_ID", "macbook")
	t.Setenv("LOCAL_DIR", t.TempDir())
	t.Setenv("COUCHDB_URL", "http://localhost:5984")
	for _, key := range []string{
		"COUCHDB_DATABASE", "COUCHDB_USER", "COUCHDB_PASSWORD", "POLL_INTERVAL",
		"DRY_RUN", "INCLUDE_EXTS", "IGNORE", "LOG_LEVEL",
	} {
		t.Setenv(key, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database != "orgsync" || cfg.PollInterval != 5*time.Second || cfg.LogLevel != "info" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.IncludeExts, []string{".org", ".md", ".txt"}) {
		t.Fatalf("IncludeExts = %#v", cfg.IncludeExts)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Setenv("DEVICE_ID", "macbook")
	t.Setenv("LOCAL_DIR", t.TempDir())
	t.Setenv("COUCHDB_URL", "http://localhost:5984")
	t.Setenv("POLL_INTERVAL", "")
	t.Setenv("DRY_RUN", "")

	for _, test := range []struct {
		name  string
		key   string
		value string
	}{
		{name: "poll interval", key: "POLL_INTERVAL", value: "later"},
		{name: "dry run", key: "DRY_RUN", value: "sometimes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func mustAbs(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

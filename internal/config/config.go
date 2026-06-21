package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DeviceID     string
	LocalDir     string
	CouchDBURL   string
	Database     string
	Username     string
	Password     string
	PollInterval time.Duration
	DryRun       bool
	IncludeExts  []string
	Ignore       []string
	LogLevel     string
}

func Load() (Config, error) {
	cfg := Config{
		DeviceID:     firstNonEmpty(os.Getenv("DEVICE_ID"), hostname()),
		LocalDir:     os.Getenv("LOCAL_DIR"),
		CouchDBURL:   os.Getenv("COUCHDB_URL"),
		Database:     firstNonEmpty(os.Getenv("COUCHDB_DATABASE"), "orgsync"),
		Username:     os.Getenv("COUCHDB_USER"),
		Password:     os.Getenv("COUCHDB_PASSWORD"),
		PollInterval: 5 * time.Second,
		IncludeExts:  splitList(os.Getenv("INCLUDE_EXTS"), []string{".org", ".md", ".txt"}),
		Ignore:       splitList(os.Getenv("IGNORE"), []string{".git", ".DS_Store", "*.tmp"}),
		LogLevel:     firstNonEmpty(os.Getenv("LOG_LEVEL"), "info"),
	}

	if value := os.Getenv("POLL_INTERVAL"); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse POLL_INTERVAL: %w", err)
		}
		cfg.PollInterval = d
	}
	if value := os.Getenv("DRY_RUN"); value != "" {
		dryRun, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse DRY_RUN: %w", err)
		}
		cfg.DryRun = dryRun
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	abs, err := filepath.Abs(cfg.LocalDir)
	if err != nil {
		return Config{}, err
	}
	cfg.LocalDir = filepath.Clean(abs)
	return cfg, nil
}

func splitList(value string, defaults []string) []string {
	if value == "" {
		return defaults
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}

func (c Config) Validate() error {
	if c.DeviceID == "" {
		return errors.New("DEVICE_ID is required when hostname is unavailable")
	}
	if c.LocalDir == "" {
		return errors.New("LOCAL_DIR is required")
	}
	if c.CouchDBURL == "" {
		return errors.New("COUCHDB_URL is required")
	}
	if c.Database == "" {
		return errors.New("COUCHDB_DATABASE is required")
	}
	if c.PollInterval <= 0 {
		return errors.New("POLL_INTERVAL must be positive")
	}
	return nil
}

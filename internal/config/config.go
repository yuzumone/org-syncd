package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DeviceID     string        `yaml:"device_id"`
	LocalDir     string        `yaml:"local_dir"`
	CouchDBURL   string        `yaml:"couchdb_url"`
	Database     string        `yaml:"database"`
	Username     string        `yaml:"username"`
	Password     string        `yaml:"password"`
	PollInterval time.Duration `yaml:"poll_interval"`
	DryRun       bool          `yaml:"-"`
	IncludeExts  []string      `yaml:"include_exts"`
	Ignore       []string      `yaml:"ignore"`
	LogLevel     string        `yaml:"log_level"`
}

type rawConfig struct {
	DeviceID     string   `yaml:"device_id"`
	LocalDir     string   `yaml:"local_dir"`
	CouchDBURL   string   `yaml:"couchdb_url"`
	Database     string   `yaml:"database"`
	Username     string   `yaml:"username"`
	Password     string   `yaml:"password"`
	PollInterval string   `yaml:"poll_interval"`
	IncludeExts  []string `yaml:"include_exts"`
	Ignore       []string `yaml:"ignore"`
	LogLevel     string   `yaml:"log_level"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, err
	}

	cfg := Config{
		DeviceID:    raw.DeviceID,
		LocalDir:    raw.LocalDir,
		CouchDBURL:  raw.CouchDBURL,
		Database:    raw.Database,
		Username:    raw.Username,
		Password:    raw.Password,
		IncludeExts: raw.IncludeExts,
		Ignore:      raw.Ignore,
		LogLevel:    raw.LogLevel,
	}

	if cfg.PollInterval == 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if raw.PollInterval != "" {
		d, err := time.ParseDuration(raw.PollInterval)
		if err != nil {
			return Config{}, fmt.Errorf("parse poll_interval: %w", err)
		}
		cfg.PollInterval = d
	}
	if len(cfg.IncludeExts) == 0 {
		cfg.IncludeExts = []string{".org", ".md", ".txt"}
	}
	if len(cfg.Ignore) == 0 {
		cfg.Ignore = []string{".git", ".DS_Store", "*.tmp"}
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
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

func (c Config) Validate() error {
	if c.DeviceID == "" {
		return errors.New("device_id is required")
	}
	if c.LocalDir == "" {
		return errors.New("local_dir is required")
	}
	if c.CouchDBURL == "" {
		return errors.New("couchdb_url is required")
	}
	if c.Database == "" {
		return errors.New("database is required")
	}
	if c.PollInterval <= 0 {
		return errors.New("poll_interval must be positive")
	}
	return nil
}

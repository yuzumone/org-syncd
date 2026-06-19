package cli

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/yuzumone/org-syncd/internal/config"
	"github.com/yuzumone/org-syncd/internal/couchdb"
	"github.com/yuzumone/org-syncd/internal/logging"
	"github.com/yuzumone/org-syncd/internal/mcpserver"
	"github.com/yuzumone/org-syncd/internal/orgvault"
	"github.com/yuzumone/org-syncd/internal/syncer"
)

var (
	configPath    string
	dryRun        bool
	mcpDeviceID   string
	mcpCouchDBURL string
	mcpDatabase   string
	mcpUsername   string
	mcpPassword   string
)

func Execute() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "org-syncd",
		Short: "Sync org-mode text files with CouchDB",
	}
	cmd.PersistentFlags().StringVarP(&configPath, "config", "c", "config.yaml", "config file path")
	cmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "log planned writes without changing CouchDB or local files")

	cmd.AddCommand(newScanCommand())
	cmd.AddCommand(newDownloadOnlyCommand())
	cmd.AddCommand(newSyncCommand())
	cmd.AddCommand(newDaemonCommand())
	cmd.AddCommand(newMCPCommand())
	return cmd
}

func newScanCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "scan",
		Short: "Scan local files and print a summary",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, log, err := load()
			if err != nil {
				return err
			}
			s, err := newSyncer(cfg, log)
			if err != nil {
				return err
			}
			local, err := s.ScanLocal()
			if err != nil {
				return err
			}
			for _, f := range local {
				log.Info("found", "path", f.Path, "sha", f.ContentSHA256)
			}
			return nil
		},
	}
}

func newDownloadOnlyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "download-only",
		Short: "Pull CouchDB file documents to the local directory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, log, err := load()
			if err != nil {
				return err
			}
			s, err := newSyncer(cfg, log)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if err := s.EnsureDB(ctx); err != nil {
				return err
			}
			return s.DownloadOnly(ctx)
		},
	}
}

func newSyncCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Run one sync cycle",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, log, err := load()
			if err != nil {
				return err
			}
			s, err := newSyncer(cfg, log)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if err := s.EnsureDB(ctx); err != nil {
				return err
			}
			return s.SyncOnce(ctx)
		},
	}
}

func newDaemonCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run continuous scan and CouchDB changes polling",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, log, err := load()
			if err != nil {
				return err
			}
			s, err := newSyncer(cfg, log)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if err := s.EnsureDB(ctx); err != nil {
				return err
			}
			log.Info("daemon started", "poll_interval", cfg.PollInterval.String())
			return s.RunDaemon(ctx)
		},
	}
}

func newMCPCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run an Org vault MCP server over stdio",
		RunE: func(cmd *cobra.Command, _ []string) error {
			vault, err := newMCPVault(cmd)
			if err != nil {
				return err
			}
			return mcpserver.New(vault, os.Stdin, os.Stdout).Serve()
		},
	}
	cmd.Flags().StringVar(&mcpDeviceID, "device-id", "", "device ID for CouchDB updated_by (defaults to DEVICE_ID or hostname)")
	cmd.Flags().StringVar(&mcpCouchDBURL, "couchdb-url", "", "CouchDB URL for MCP (defaults to COUCHDB_URL)")
	cmd.Flags().StringVar(&mcpDatabase, "database", "", "CouchDB database for MCP (defaults to COUCHDB_DATABASE, DATABASE, or orgsync)")
	cmd.Flags().StringVar(&mcpUsername, "username", "", "CouchDB username for MCP (defaults to COUCHDB_USER or COUCHDB_USERNAME)")
	cmd.Flags().StringVar(&mcpPassword, "password", "", "CouchDB password for MCP (defaults to COUCHDB_PASSWORD)")
	return cmd
}

func newMCPVault(cmd *cobra.Command) (*orgvault.CouchDBBackend, error) {
	couchURL := firstNonEmpty(mcpCouchDBURL, os.Getenv("COUCHDB_URL"))
	if couchURL == "" {
		return nil, fmt.Errorf("COUCHDB_URL or --couchdb-url is required")
	}

	database := firstNonEmpty(mcpDatabase, os.Getenv("COUCHDB_DATABASE"), os.Getenv("DATABASE"), "orgsync")
	username := firstNonEmpty(mcpUsername, os.Getenv("COUCHDB_USER"), os.Getenv("COUCHDB_USERNAME"))
	password := firstNonEmpty(mcpPassword, os.Getenv("COUCHDB_PASSWORD"))
	deviceID := firstNonEmpty(mcpDeviceID, os.Getenv("DEVICE_ID"), hostname(), "mcp")
	client, err := couchdb.New(couchURL, database, username, password)
	if err != nil {
		return nil, err
	}
	return orgvault.NewCouchDBBackend(client, deviceID), nil
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

func load() (config.Config, *slog.Logger, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return config.Config{}, nil, err
	}
	if dryRun {
		cfg.DryRun = true
	}
	log := logging.New(cfg.LogLevel)
	return cfg, log, nil
}

func newSyncer(cfg config.Config, log *slog.Logger) (*syncer.Syncer, error) {
	client, err := couchdb.New(cfg.CouchDBURL, cfg.Database, cfg.Username, cfg.Password)
	if err != nil {
		return nil, err
	}
	return syncer.New(cfg, client, log), nil
}

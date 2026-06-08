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
	"github.com/yuzumone/org-syncd/internal/syncer"
)

var (
	configPath string
	dryRun     bool
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

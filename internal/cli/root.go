package cli

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/yuzumone/org-syncd/internal/config"
	"github.com/yuzumone/org-syncd/internal/couchdb"
	"github.com/yuzumone/org-syncd/internal/logging"
	"github.com/yuzumone/org-syncd/internal/mcpserver"
	"github.com/yuzumone/org-syncd/internal/orgvault"
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
		Short: "Run an Org vault MCP server over HTTP",
		RunE: func(cmd *cobra.Command, _ []string) error {
			vault, err := newMCPVault()
			if err != nil {
				return err
			}
			host := firstNonEmpty(os.Getenv("HOST"), "0.0.0.0")
			port := firstNonEmpty(os.Getenv("PORT"), "8080")
			auth, err := newMCPAuth(port)
			if err != nil {
				return err
			}
			handler := mcpserver.HTTPHandler(vault, auth)
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			listen := net.JoinHostPort(host, port)
			server := &http.Server{Addr: listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
			go func() {
				<-ctx.Done()
				if err := auth.Save(); err != nil {
					slog.Error("failed to save OAuth state", "error", err)
				}
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = server.Shutdown(shutdownCtx)
			}()
			slog.Info("MCP HTTP server started", "listen", listen, "path", mcpserver.EndpointPath)
			err = server.ListenAndServe()
			if err == http.ErrServerClosed {
				return nil
			}
			return err
		},
	}
	return cmd
}

func newMCPAuth(port string) (*mcpserver.OAuthProvider, error) {
	refreshDays := 14
	if value := os.Getenv("MCP_REFRESH_DAYS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("MCP_REFRESH_DAYS must be a positive integer")
		}
		refreshDays = parsed
	}
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve DATA_DIR: %w", err)
		}
		dataDir = filepath.Join(home, ".org-syncd")
	}
	baseURL := firstNonEmpty(os.Getenv("BASE_URL"), "http://localhost:"+port)
	return mcpserver.NewOAuthProvider(mcpserver.OAuthConfig{
		BaseURL:    baseURL,
		Password:   os.Getenv("MCP_AUTH_TOKEN"),
		DataDir:    dataDir,
		RefreshTTL: time.Duration(refreshDays) * 24 * time.Hour,
	})
}

func newMCPVault() (*orgvault.CouchDBBackend, error) {
	couchURL := os.Getenv("COUCHDB_URL")
	if couchURL == "" {
		return nil, fmt.Errorf("COUCHDB_URL is required")
	}

	database := firstNonEmpty(os.Getenv("COUCHDB_DATABASE"), "orgsync")
	username := os.Getenv("COUCHDB_USER")
	password := os.Getenv("COUCHDB_PASSWORD")
	deviceID := firstNonEmpty(os.Getenv("DEVICE_ID"), hostname(), "mcp")
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

package syncer

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/yuzumone/org-syncd/internal/config"
	"github.com/yuzumone/org-syncd/internal/couchdb"
	"github.com/yuzumone/org-syncd/internal/files"
	"github.com/yuzumone/org-syncd/internal/watcher"
)

type Syncer struct {
	cfg     config.Config
	client  *couchdb.Client
	log     *slog.Logger
	matcher files.Matcher
	state   map[string]string
	since   string
}

func New(cfg config.Config, client *couchdb.Client, log *slog.Logger) *Syncer {
	return &Syncer{
		cfg:     cfg,
		client:  client,
		log:     log,
		matcher: files.NewMatcher(cfg.IncludeExts, cfg.Ignore),
		state:   map[string]string{},
	}
}

func (s *Syncer) EnsureDB(ctx context.Context) error {
	if s.cfg.DryRun {
		s.log.Info("dry-run ensure database", "database", s.cfg.Database)
		return nil
	}
	return s.client.EnsureDB(ctx)
}

func (s *Syncer) ScanLocal() ([]files.LocalFile, error) {
	local, err := files.Scan(s.cfg.LocalDir, s.matcher)
	if err != nil {
		return nil, err
	}
	s.log.Info("scan summary", "files", len(local))
	return local, nil
}

func (s *Syncer) DownloadOnly(ctx context.Context) error {
	docs, err := s.client.AllDocs(ctx)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		if !s.validRemoteDoc(doc) {
			continue
		}
		if doc.Deleted {
			s.log.Info("skip deleted remote", "path", doc.Path, "rev", doc.Rev)
			continue
		}
		if err := s.pullDoc(doc); err != nil {
			return err
		}
	}
	return nil
}

func (s *Syncer) SyncOnce(ctx context.Context) error {
	local, err := s.ScanLocal()
	if err != nil {
		return err
	}
	localByPath := files.MapByPath(local)

	remoteDocs, err := s.client.AllDocs(ctx)
	if err != nil {
		return err
	}
	remoteByPath := map[string]couchdb.FileDoc{}
	for _, doc := range remoteDocs {
		if s.validRemoteDoc(doc) {
			remoteByPath[doc.Path] = doc
		}
	}

	for path, lf := range localByPath {
		doc, ok := remoteByPath[path]
		if !ok {
			if err := s.pushFile(ctx, lf, "local-only"); err != nil {
				return err
			}
			continue
		}
		if lf.ContentSHA256 == doc.ContentSHA256 {
			s.state[path] = lf.ContentSHA256
			continue
		}
		if doc.UpdatedBy != s.cfg.DeviceID {
			conflict := files.ConflictPath(path, time.Now())
			if err := s.writeConflict(conflict, []byte(doc.Content)); err != nil {
				return err
			}
			s.log.Warn("conflict", "path", path, "conflict", conflict)
			continue
		}
		if doc.UpdatedBy == s.cfg.DeviceID {
			if err := s.pushFile(ctx, lf, "local-newer"); err != nil {
				return err
			}
			continue
		}
		if err := s.pullDoc(doc); err != nil {
			return err
		}
	}

	for path, doc := range remoteByPath {
		if _, ok := localByPath[path]; ok {
			continue
		}
		if doc.Deleted {
			continue
		}
		if err := s.pullDoc(doc); err != nil {
			return err
		}
	}
	return nil
}

func (s *Syncer) PollChanges(ctx context.Context) error {
	docs, since, err := s.client.Changes(ctx, s.since)
	if err != nil {
		return err
	}
	s.since = since
	for _, doc := range docs {
		if !s.validRemoteDoc(doc) {
			continue
		}
		if doc.UpdatedBy == s.cfg.DeviceID {
			continue
		}
		if doc.Deleted {
			s.log.Info("skip deleted remote", "path", doc.Path, "rev", doc.Rev)
			continue
		}
		if err := s.pullDoc(doc); err != nil {
			return err
		}
	}
	return nil
}

func (s *Syncer) RunDaemon(ctx context.Context) error {
	if err := s.SyncOnce(ctx); err != nil {
		return err
	}

	localChanged := make(chan struct{}, 1)
	watchErr := make(chan error, 1)
	w := watcher.New(s.cfg.LocalDir, s.matcher, 500*time.Millisecond, s.log)
	go func() {
		watchErr <- w.Run(ctx, localChanged)
	}()

	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-watchErr:
			return err
		case <-localChanged:
			if err := s.SyncOnce(ctx); err != nil {
				return err
			}
		case <-ticker.C:
			if err := s.PollChanges(ctx); err != nil {
				return err
			}
		}
	}
}

func (s *Syncer) pushFile(ctx context.Context, lf files.LocalFile, reason string) error {
	docID := files.DocID(lf.Path)
	existing, ok, err := s.client.GetDoc(ctx, docID)
	if err != nil {
		return err
	}
	doc := couchdb.FileDoc{
		ID:            docID,
		Type:          "file",
		Path:          lf.Path,
		Content:       string(lf.Content),
		ContentSHA256: lf.ContentSHA256,
		MTime:         lf.MTime.Format(time.RFC3339),
		Deleted:       false,
		UpdatedBy:     s.cfg.DeviceID,
	}
	if ok {
		doc.Rev = existing.Rev
	}
	if s.cfg.DryRun {
		s.log.Info("dry-run push", "path", lf.Path, "sha", lf.ContentSHA256, "reason", reason)
		return nil
	}
	rev, err := s.client.PutDoc(ctx, doc)
	if err != nil {
		return err
	}
	s.state[lf.Path] = lf.ContentSHA256
	s.log.Info("pushed", "path", lf.Path, "sha", lf.ContentSHA256, "rev", rev, "reason", reason)
	return nil
}

func (s *Syncer) pullDoc(doc couchdb.FileDoc) error {
	conflict, err := s.needsConflict(doc)
	if err != nil {
		return err
	}
	if conflict {
		conflictPath := files.ConflictPath(doc.Path, time.Now())
		if err := s.writeConflict(conflictPath, []byte(doc.Content)); err != nil {
			return err
		}
		s.log.Warn("conflict", "path", doc.Path, "conflict", conflictPath)
		return nil
	}
	if s.cfg.DryRun {
		s.log.Info("dry-run pull", "path", doc.Path, "rev", doc.Rev)
		return nil
	}
	if err := files.AtomicWrite(s.cfg.LocalDir, doc.Path, []byte(doc.Content)); err != nil {
		return err
	}
	s.state[doc.Path] = doc.ContentSHA256
	s.log.Info("pulled", "path", doc.Path, "rev", doc.Rev)
	return nil
}

func (s *Syncer) needsConflict(doc couchdb.FileDoc) (bool, error) {
	target := filepath.Join(s.cfg.LocalDir, filepath.FromSlash(doc.Path))
	data, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	localSHA := files.SHA256Hex(data)
	if localSHA == doc.ContentSHA256 {
		return false, nil
	}
	lastKnown := s.state[doc.Path]
	if lastKnown == "" {
		return true, nil
	}
	return localSHA != lastKnown, nil
}

func (s *Syncer) writeConflict(path string, content []byte) error {
	if s.cfg.DryRun {
		s.log.Warn("dry-run conflict", "conflict", path)
		return nil
	}
	return files.AtomicWrite(s.cfg.LocalDir, path, content)
}

func (s *Syncer) validRemoteDoc(doc couchdb.FileDoc) bool {
	if doc.Type != "file" || doc.Path == "" {
		return false
	}
	rel, err := files.NormalizeRelativePath(s.cfg.LocalDir, doc.Path)
	if err != nil || rel != doc.Path {
		s.log.Warn("skipped remote path", "path", doc.Path)
		return false
	}
	if !s.matcher.Include(doc.Path) {
		s.log.Warn("skipped remote path", "path", doc.Path)
		return false
	}
	return true
}

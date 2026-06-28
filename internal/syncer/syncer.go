package syncer

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	deleted map[string]bool
	since   string
}

func New(cfg config.Config, client *couchdb.Client, log *slog.Logger) *Syncer {
	return &Syncer{
		cfg:     cfg,
		client:  client,
		log:     log,
		matcher: files.NewMatcher(cfg.IncludeExts, cfg.Ignore),
		state:   map[string]string{},
		deleted: map[string]bool{},
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
	return s.syncOnce(ctx, true)
}

func (s *Syncer) SyncLocalOnlyOnce(ctx context.Context) error {
	return s.syncOnce(ctx, false)
}

func (s *Syncer) syncOnce(ctx context.Context, pullRemoteOnly bool) error {
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
	remoteDocsByPath := map[string][]couchdb.FileDoc{}
	for _, doc := range remoteDocs {
		if s.validRemoteDoc(doc) {
			remoteDocsByPath[doc.Path] = append(remoteDocsByPath[doc.Path], doc)
			s.addRemoteDoc(remoteByPath, doc)
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
		if doc.Deleted {
			if err := s.applyRemoteDelete(doc); err != nil {
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

	for path, docsForPath := range remoteDocsByPath {
		if _, ok := localByPath[path]; ok {
			continue
		}
		doc, ok := remoteByPath[path]
		if !ok {
			continue
		}
		lastKnown := s.state[path]
		if lastKnown != "" || s.deleted[path] {
			if err := s.markAllDeleted(ctx, path, docsForPath); err != nil {
				return err
			}
			continue
		}
		if doc.Deleted {
			if err := s.markAllDeleted(ctx, path, docsForPath); err != nil {
				return err
			}
			continue
		}
		if !pullRemoteOnly {
			s.log.Info("skip remote-only during local-only sync", "path", path, "rev", doc.Rev)
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
			if err := s.applyRemoteDelete(doc); err != nil {
				return err
			}
			continue
		}
		if err := s.pullDoc(doc); err != nil {
			return err
		}
	}
	return nil
}

func (s *Syncer) RunDaemon(ctx context.Context) error {
	if err := s.initializeChangesCursor(ctx); err != nil {
		return err
	}
	if err := s.SyncLocalOnlyOnce(ctx); err != nil {
		return err
	}

	localChanged := make(chan []watcher.Event, 1)
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
		case events := <-localChanged:
			for _, event := range events {
				if err := s.HandleLocalEvent(ctx, event); err != nil {
					return err
				}
			}
		case <-ticker.C:
			if err := s.PollChanges(ctx); err != nil {
				return err
			}
		}
	}
}

func (s *Syncer) HandleLocalEvent(ctx context.Context, event watcher.Event) error {
	switch event.Op {
	case watcher.OpCreate, watcher.OpWrite:
		return s.pushPath(ctx, event.Path, string(event.Op))
	case watcher.OpDelete:
		return s.markPathDeleted(ctx, event.Path)
	default:
		return nil
	}
}

func (s *Syncer) initializeChangesCursor(ctx context.Context) error {
	if s.since != "" {
		return nil
	}
	_, since, err := s.client.Changes(ctx, "now")
	if err != nil {
		return err
	}
	s.since = since
	return nil
}

func (s *Syncer) pushPath(ctx context.Context, path, reason string) error {
	rel, err := files.NormalizeRelativePath(s.cfg.LocalDir, path)
	if err != nil {
		return err
	}
	if !s.matcher.Include(rel) {
		return nil
	}
	abs := filepath.Join(s.cfg.LocalDir, filepath.FromSlash(rel))
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return s.markPathDeleted(ctx, rel)
		}
		return err
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return s.markPathDeleted(ctx, rel)
		}
		return err
	}
	sha := files.SHA256Hex(data)
	if s.state[rel] == sha && !s.deleted[rel] {
		s.log.Info("skipped unchanged local event", "path", rel, "sha", sha, "reason", reason)
		return nil
	}
	return s.pushFile(ctx, files.LocalFile{
		Path:          rel,
		AbsolutePath:  abs,
		Content:       data,
		ContentSHA256: sha,
		MTime:         info.ModTime(),
	}, reason)
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
	delete(s.deleted, lf.Path)
	s.log.Info("pushed", "path", lf.Path, "sha", lf.ContentSHA256, "rev", rev, "reason", reason)
	return nil
}

func (s *Syncer) markPathDeleted(ctx context.Context, path string) error {
	rel, err := files.NormalizeRelativePath(s.cfg.LocalDir, path)
	if err != nil {
		return err
	}
	docs, err := s.client.AllDocs(ctx)
	if err != nil {
		return err
	}
	var docsForPath []couchdb.FileDoc
	for _, doc := range docs {
		if s.validRemoteDoc(doc) && pathMatchesDeleteEvent(doc.Path, rel) {
			docsForPath = append(docsForPath, doc)
		}
	}
	if len(docsForPath) == 0 {
		delete(s.state, rel)
		s.deleted[rel] = true
		s.log.Info("marked absent local path deleted with no remote doc", "path", rel)
		return nil
	}
	return s.markAllDeleted(ctx, rel, docsForPath)
}

func pathMatchesDeleteEvent(docPath, deletedPath string) bool {
	return docPath == deletedPath || strings.HasPrefix(docPath, strings.TrimRight(deletedPath, "/")+"/")
}

func (s *Syncer) markAllDeleted(ctx context.Context, path string, docs []couchdb.FileDoc) error {
	for _, doc := range docs {
		if doc.Deleted {
			continue
		}
		if err := s.markDeleted(ctx, doc); err != nil {
			return err
		}
	}
	delete(s.state, path)
	s.deleted[path] = true
	return nil
}

func (s *Syncer) markDeleted(ctx context.Context, doc couchdb.FileDoc) error {
	doc.Deleted = true
	doc.UpdatedBy = s.cfg.DeviceID
	doc.MTime = time.Now().Format(time.RFC3339)
	if s.cfg.DryRun {
		s.log.Info("dry-run deleted remote", "path", doc.Path, "rev", doc.Rev)
		return nil
	}
	rev, err := s.client.PutDoc(ctx, doc)
	if err != nil {
		return err
	}
	delete(s.state, doc.Path)
	s.deleted[doc.Path] = true
	s.log.Info("deleted remote", "path", doc.Path, "rev", rev)
	return nil
}

func (s *Syncer) applyRemoteDelete(doc couchdb.FileDoc) error {
	target := filepath.Join(s.cfg.LocalDir, filepath.FromSlash(doc.Path))
	data, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			delete(s.state, doc.Path)
			s.deleted[doc.Path] = true
			s.log.Info("remote delete already absent locally", "path", doc.Path, "rev", doc.Rev)
			return nil
		}
		return err
	}

	localSHA := files.SHA256Hex(data)
	lastKnown := s.state[doc.Path]
	if lastKnown != "" && localSHA != lastKnown {
		s.log.Warn("remote delete conflict", "path", doc.Path, "rev", doc.Rev)
		return nil
	}
	if lastKnown == "" && doc.ContentSHA256 != "" && localSHA != doc.ContentSHA256 {
		s.log.Warn("remote delete conflict", "path", doc.Path, "rev", doc.Rev)
		return nil
	}
	if s.cfg.DryRun {
		s.log.Info("dry-run remove local deleted remote", "path", doc.Path, "rev", doc.Rev)
		return nil
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(s.state, doc.Path)
	s.deleted[doc.Path] = true
	s.log.Info("removed local deleted remote", "path", doc.Path, "rev", doc.Rev)
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
	delete(s.deleted, doc.Path)
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
	expectedID := files.DocID(doc.Path)
	if doc.ID != expectedID && !isLegacyEscapedDocID(doc.ID, expectedID) {
		s.log.Warn("skipped remote doc with non-canonical id", "path", doc.Path, "id", doc.ID, "expected_id", expectedID)
		return false
	}
	if !s.matcher.Include(doc.Path) {
		s.log.Warn("skipped remote path", "path", doc.Path)
		return false
	}
	return true
}

func (s *Syncer) addRemoteDoc(remoteByPath map[string]couchdb.FileDoc, doc couchdb.FileDoc) {
	existing, ok := remoteByPath[doc.Path]
	if !ok {
		remoteByPath[doc.Path] = doc
		return
	}
	if !existing.Deleted && doc.Deleted {
		remoteByPath[doc.Path] = doc
		return
	}
	if existing.Deleted && !doc.Deleted {
		s.log.Info("ignored active remote doc because deleted doc exists", "path", doc.Path, "id", doc.ID, "deleted_id", existing.ID)
		return
	}
	expectedID := files.DocID(doc.Path)
	if existing.ID != expectedID && doc.ID == expectedID {
		remoteByPath[doc.Path] = doc
		return
	}
	if existing.ID == expectedID && doc.ID != expectedID {
		s.log.Info("ignored legacy escaped remote doc because canonical doc exists", "path", doc.Path, "id", doc.ID, "canonical_id", expectedID)
		return
	}
	remoteByPath[doc.Path] = doc
}

func isLegacyEscapedDocID(id, expectedID string) bool {
	decoded, err := url.PathUnescape(id)
	return err == nil && decoded == expectedID
}

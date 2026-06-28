package syncer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuzumone/org-syncd/internal/config"
	"github.com/yuzumone/org-syncd/internal/couchdb"
	"github.com/yuzumone/org-syncd/internal/files"
	"github.com/yuzumone/org-syncd/internal/watcher"
)

func TestSyncOnceMarksKnownLocalDeletionRemoteDeleted(t *testing.T) {
	localDir := t.TempDir()
	content := []byte("* TODO old\n")
	if err := os.WriteFile(filepath.Join(localDir, "tasks.org"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	path := "tasks.org"
	docID := files.DocID(path)
	remoteDocs := map[string]couchdb.FileDoc{
		docID: {
			ID:            docID,
			Rev:           "1-old",
			Type:          "file",
			Path:          path,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			UpdatedBy:     "macbook",
		},
	}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(localDir, path)); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(localDir, path)); !os.IsNotExist(err) {
		t.Fatalf("local file was restored, stat err = %v", err)
	}
	if !remoteDocs[docID].Deleted {
		t.Fatalf("remote doc was not marked deleted: %+v", remoteDocs[docID])
	}
	if remoteDocs[docID].UpdatedBy != "macbook" {
		t.Fatalf("updated_by = %q", remoteDocs[docID].UpdatedBy)
	}
}

func TestSyncOnceHandlesLocalMoveAsNewPathAndDeletedOldPath(t *testing.T) {
	localDir := t.TempDir()
	content := []byte("* TODO moved\n")
	oldPath := "website/20251208224418-how_to_control_image_sizes_inside_of_table.org"
	newPath := "roam/" + oldPath
	oldAbs := filepath.Join(localDir, filepath.FromSlash(oldPath))
	newAbs := filepath.Join(localDir, filepath.FromSlash(newPath))
	if err := os.MkdirAll(filepath.Dir(oldAbs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldAbs, content, 0o644); err != nil {
		t.Fatal(err)
	}

	oldDocID := files.DocID(oldPath)
	newDocID := files.DocID(newPath)
	remoteDocs := map[string]couchdb.FileDoc{
		oldDocID: {
			ID:            oldDocID,
			Rev:           "1-old",
			Type:          "file",
			Path:          oldPath,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			UpdatedBy:     "macbook",
		},
	}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(oldAbs); !os.IsNotExist(err) {
		t.Fatalf("old path was restored, stat err = %v", err)
	}
	if !remoteDocs[oldDocID].Deleted {
		t.Fatalf("old remote doc was not marked deleted: %+v", remoteDocs[oldDocID])
	}
	if remoteDocs[newDocID].Deleted || remoteDocs[newDocID].Path != newPath {
		t.Fatalf("new remote doc was not created correctly: %+v", remoteDocs[newDocID])
	}
}

func TestInitializeChangesCursorSkipsPastChanges(t *testing.T) {
	localDir := t.TempDir()
	path := "tasks.org"
	content := []byte("* TODO old\n")
	docID := files.DocID(path)
	remoteDocs := map[string]couchdb.FileDoc{
		docID: {
			ID:            docID,
			Rev:           "1-old",
			Type:          "file",
			Path:          path,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			UpdatedBy:     "other-device",
		},
	}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())

	if err := s.initializeChangesCursor(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.PollChanges(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(localDir, path)); !os.IsNotExist(err) {
		t.Fatalf("stale change was pulled, stat err = %v", err)
	}
}

func TestSyncLocalOnlyOnceDoesNotPullRemoteOnlyDoc(t *testing.T) {
	localDir := t.TempDir()
	path := "website/remote-only.org"
	content := []byte("* Remote only\n")
	docID := files.DocID(path)
	remoteDocs := map[string]couchdb.FileDoc{
		docID: {
			ID:            docID,
			Rev:           "1-remote",
			Type:          "file",
			Path:          path,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			UpdatedBy:     "other-device",
		},
	}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())

	if err := s.SyncLocalOnlyOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(localDir, filepath.FromSlash(path))); !os.IsNotExist(err) {
		t.Fatalf("remote-only doc was pulled during local-only sync, stat err = %v", err)
	}
}

func TestSyncOnceStillPullsRemoteOnlyDoc(t *testing.T) {
	localDir := t.TempDir()
	path := "website/remote-only.org"
	content := []byte("* Remote only\n")
	docID := files.DocID(path)
	remoteDocs := map[string]couchdb.FileDoc{
		docID: {
			ID:            docID,
			Rev:           "1-remote",
			Type:          "file",
			Path:          path,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			UpdatedBy:     "other-device",
		},
	}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(localDir, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("remote-only content = %q", string(got))
	}
}

func TestPollChangesAppliesRemoteDeleteWhenLocalUnchanged(t *testing.T) {
	localDir := t.TempDir()
	path := "tasks.org"
	content := []byte("* TODO old\n")
	if err := os.WriteFile(filepath.Join(localDir, path), content, 0o644); err != nil {
		t.Fatal(err)
	}
	docID := files.DocID(path)
	remoteDocs := map[string]couchdb.FileDoc{
		docID: {
			ID:            docID,
			Rev:           "2-deleted",
			Type:          "file",
			Path:          path,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			Deleted:       true,
			UpdatedBy:     "other-device",
		},
	}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())
	s.state[path] = files.SHA256Hex(content)

	if err := s.PollChanges(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(localDir, path)); !os.IsNotExist(err) {
		t.Fatalf("deleted remote was not removed locally, stat err = %v", err)
	}
}

func TestPollChangesKeepsLocalModifiedFileOnRemoteDelete(t *testing.T) {
	localDir := t.TempDir()
	path := "tasks.org"
	oldContent := []byte("* TODO old\n")
	newContent := []byte("* TODO local edit\n")
	if err := os.WriteFile(filepath.Join(localDir, path), newContent, 0o644); err != nil {
		t.Fatal(err)
	}
	docID := files.DocID(path)
	remoteDocs := map[string]couchdb.FileDoc{
		docID: {
			ID:            docID,
			Rev:           "2-deleted",
			Type:          "file",
			Path:          path,
			Content:       string(oldContent),
			ContentSHA256: files.SHA256Hex(oldContent),
			MTime:         time.Now().Format(time.RFC3339),
			Deleted:       true,
			UpdatedBy:     "other-device",
		},
	}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())
	s.state[path] = files.SHA256Hex(oldContent)

	if err := s.PollChanges(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(localDir, path))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newContent) {
		t.Fatalf("local modified file changed: %q", string(got))
	}
}

func TestSyncOnceIgnoresNonCanonicalRemoteDocForSamePath(t *testing.T) {
	localDir := t.TempDir()
	path := "roam/2026_archive.org"
	content := []byte("* Archive\n")
	canonicalID := files.DocID(path)
	staleID := "file:stale/" + path
	remoteDocs := map[string]couchdb.FileDoc{
		canonicalID: {
			ID:            canonicalID,
			Rev:           "2-deleted",
			Type:          "file",
			Path:          path,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			Deleted:       true,
			UpdatedBy:     "macbook",
		},
		staleID: {
			ID:            staleID,
			Rev:           "1-stale",
			Type:          "file",
			Path:          path,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			UpdatedBy:     "macbook",
		},
	}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(localDir, filepath.FromSlash(path))); !os.IsNotExist(err) {
		t.Fatalf("non-canonical remote doc was pulled, stat err = %v", err)
	}
}

func TestSyncOnceAcceptsLegacyEscapedRemoteDocID(t *testing.T) {
	localDir := t.TempDir()
	path := "roam/20251208213248-dart.org"
	content := []byte("* Dart\n")
	legacyID := "file:roam%2F20251208213248-dart.org"
	remoteDocs := map[string]couchdb.FileDoc{
		legacyID: {
			ID:            legacyID,
			Rev:           "1-legacy",
			Type:          "file",
			Path:          path,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			UpdatedBy:     "macbook",
		},
	}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(localDir, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("legacy escaped doc content = %q", string(got))
	}
}

func TestSyncOncePrefersCanonicalDeletedDocOverLegacyEscapedActiveDoc(t *testing.T) {
	localDir := t.TempDir()
	path := "roam/2026_archive.org"
	content := []byte("* Archive\n")
	canonicalID := files.DocID(path)
	legacyID := "file:roam%2F2026_archive.org"
	remoteDocs := map[string]couchdb.FileDoc{
		canonicalID: {
			ID:            canonicalID,
			Rev:           "2-deleted",
			Type:          "file",
			Path:          path,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			Deleted:       true,
			UpdatedBy:     "macbook",
		},
		legacyID: {
			ID:            legacyID,
			Rev:           "1-legacy",
			Type:          "file",
			Path:          path,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			UpdatedBy:     "macbook",
		},
	}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(localDir, filepath.FromSlash(path))); !os.IsNotExist(err) {
		t.Fatalf("legacy escaped active doc was pulled over canonical delete, stat err = %v", err)
	}
}

func TestSyncLocalOnlyOncePrefersDeletedDocOverActiveDuplicate(t *testing.T) {
	localDir := t.TempDir()
	path := "website/deleted.org"
	content := []byte("* Deleted\n")
	canonicalID := files.DocID(path)
	legacyID := "file:website%2Fdeleted.org"
	remoteDocs := map[string]couchdb.FileDoc{
		canonicalID: {
			ID:            canonicalID,
			Rev:           "2-deleted",
			Type:          "file",
			Path:          path,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			Deleted:       true,
			UpdatedBy:     "macbook",
		},
		legacyID: {
			ID:            legacyID,
			Rev:           "1-legacy",
			Type:          "file",
			Path:          path,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			UpdatedBy:     "macbook",
		},
	}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())

	if err := s.SyncLocalOnlyOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(localDir, filepath.FromSlash(path))); !os.IsNotExist(err) {
		t.Fatalf("active duplicate was pulled despite deleted doc, stat err = %v", err)
	}
	if !remoteDocs[legacyID].Deleted {
		t.Fatalf("active duplicate was not tombstoned: %+v", remoteDocs[legacyID])
	}
}

func TestSyncOnceDeletesAllRemoteDocsForDeletedLocalPath(t *testing.T) {
	localDir := t.TempDir()
	path := "roam/20250427120847-revisiting_android_spinners.org"
	content := []byte("* Spinner\n")
	target := filepath.Join(localDir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		t.Fatal(err)
	}

	canonicalID := files.DocID(path)
	legacyID := "file:roam%2F20250427120847-revisiting_android_spinners.org"
	remoteDocs := map[string]couchdb.FileDoc{
		canonicalID: {
			ID:            canonicalID,
			Rev:           "4-canonical",
			Type:          "file",
			Path:          path,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			UpdatedBy:     "macbook",
		},
		legacyID: {
			ID:            legacyID,
			Rev:           "1-legacy",
			Type:          "file",
			Path:          path,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			UpdatedBy:     "macbook",
		},
	}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !remoteDocs[canonicalID].Deleted {
		t.Fatalf("canonical doc was not deleted: %+v", remoteDocs[canonicalID])
	}
	if !remoteDocs[legacyID].Deleted {
		t.Fatalf("legacy doc was not deleted: %+v", remoteDocs[legacyID])
	}
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("deleted path was restored, stat err = %v", err)
	}
}

func TestHandleLocalEventPushesCreatedFile(t *testing.T) {
	localDir := t.TempDir()
	path := "tasks.org"
	content := []byte("* TODO created\n")
	if err := os.WriteFile(filepath.Join(localDir, path), content, 0o644); err != nil {
		t.Fatal(err)
	}
	remoteDocs := map[string]couchdb.FileDoc{}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())

	if err := s.HandleLocalEvent(context.Background(), watcher.Event{Path: path, Op: watcher.OpCreate}); err != nil {
		t.Fatal(err)
	}

	doc := remoteDocs[files.DocID(path)]
	if doc.Path != path || doc.Content != string(content) || doc.Deleted {
		t.Fatalf("created file was not pushed: %+v", doc)
	}
}

func TestHandleLocalEventPushesWrittenFile(t *testing.T) {
	localDir := t.TempDir()
	path := "tasks.org"
	oldContent := []byte("* TODO old\n")
	newContent := []byte("* TODO new\n")
	if err := os.WriteFile(filepath.Join(localDir, path), oldContent, 0o644); err != nil {
		t.Fatal(err)
	}
	docID := files.DocID(path)
	remoteDocs := map[string]couchdb.FileDoc{
		docID: {
			ID:            docID,
			Rev:           "1-old",
			Type:          "file",
			Path:          path,
			Content:       string(oldContent),
			ContentSHA256: files.SHA256Hex(oldContent),
			MTime:         time.Now().Format(time.RFC3339),
			UpdatedBy:     "macbook",
		},
	}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())
	if err := os.WriteFile(filepath.Join(localDir, path), newContent, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.HandleLocalEvent(context.Background(), watcher.Event{Path: path, Op: watcher.OpWrite}); err != nil {
		t.Fatal(err)
	}

	doc := remoteDocs[docID]
	if doc.Content != string(newContent) || doc.ContentSHA256 != files.SHA256Hex(newContent) {
		t.Fatalf("written file was not pushed: %+v", doc)
	}
}

func TestHandleLocalEventSkipsUnchangedWrittenFile(t *testing.T) {
	localDir := t.TempDir()
	path := "tasks.org"
	content := []byte("* TODO unchanged\n")
	if err := os.WriteFile(filepath.Join(localDir, path), content, 0o644); err != nil {
		t.Fatal(err)
	}
	docID := files.DocID(path)
	remoteDocs := map[string]couchdb.FileDoc{
		docID: {
			ID:            docID,
			Rev:           "1-old",
			Type:          "file",
			Path:          path,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			UpdatedBy:     "macbook",
		},
	}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())
	s.state[path] = files.SHA256Hex(content)

	if err := s.HandleLocalEvent(context.Background(), watcher.Event{Path: path, Op: watcher.OpWrite}); err != nil {
		t.Fatal(err)
	}

	doc := remoteDocs[docID]
	if doc.Rev != "1-old" || doc.Content != string(content) {
		t.Fatalf("unchanged write event should not push: %+v", doc)
	}
}

func TestHandleLocalEventMarksDeletedPath(t *testing.T) {
	localDir := t.TempDir()
	path := "tasks.org"
	content := []byte("* TODO old\n")
	docID := files.DocID(path)
	remoteDocs := map[string]couchdb.FileDoc{
		docID: {
			ID:            docID,
			Rev:           "1-old",
			Type:          "file",
			Path:          path,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			UpdatedBy:     "macbook",
		},
	}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())
	s.state[path] = files.SHA256Hex(content)

	if err := s.HandleLocalEvent(context.Background(), watcher.Event{Path: path, Op: watcher.OpDelete}); err != nil {
		t.Fatal(err)
	}

	if !remoteDocs[docID].Deleted {
		t.Fatalf("delete event did not mark remote deleted: %+v", remoteDocs[docID])
	}
}

func TestHandleLocalEventMarksDeletedDirectoryChildren(t *testing.T) {
	localDir := t.TempDir()
	insidePath := "website/inside.org"
	outsidePath := "roam/outside.org"
	content := []byte("* TODO old\n")
	insideID := files.DocID(insidePath)
	outsideID := files.DocID(outsidePath)
	remoteDocs := map[string]couchdb.FileDoc{
		insideID: {
			ID:            insideID,
			Rev:           "1-inside",
			Type:          "file",
			Path:          insidePath,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			UpdatedBy:     "macbook",
		},
		outsideID: {
			ID:            outsideID,
			Rev:           "1-outside",
			Type:          "file",
			Path:          outsidePath,
			Content:       string(content),
			ContentSHA256: files.SHA256Hex(content),
			MTime:         time.Now().Format(time.RFC3339),
			UpdatedBy:     "macbook",
		},
	}
	client := newTestCouchClient(t, remoteDocs)
	s := New(testConfig(localDir), client, testLogger())

	if err := s.HandleLocalEvent(context.Background(), watcher.Event{Path: "website", Op: watcher.OpDelete}); err != nil {
		t.Fatal(err)
	}

	if !remoteDocs[insideID].Deleted {
		t.Fatalf("directory child was not marked deleted: %+v", remoteDocs[insideID])
	}
	if remoteDocs[outsideID].Deleted {
		t.Fatalf("outside doc was marked deleted: %+v", remoteDocs[outsideID])
	}
}

func testConfig(localDir string) config.Config {
	return config.Config{
		DeviceID:     "macbook",
		LocalDir:     localDir,
		CouchDBURL:   "http://example.invalid",
		Database:     "orgsync",
		PollInterval: time.Second,
		IncludeExts:  []string{".org"},
		Ignore:       []string{".git", ".DS_Store", "*.tmp"},
		LogLevel:     "error",
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestCouchClient(t *testing.T, docs map[string]couchdb.FileDoc) *couchdb.Client {
	t.Helper()
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/orgsync/_all_docs":
			var rows []struct {
				ID  string          `json:"id"`
				Doc couchdb.FileDoc `json:"doc"`
			}
			for id, doc := range docs {
				rows = append(rows, struct {
					ID  string          `json:"id"`
					Doc couchdb.FileDoc `json:"doc"`
				}{ID: id, Doc: doc})
			}
			return testJSONResponse(http.StatusOK, map[string]any{"rows": rows}), nil
		case r.Method == http.MethodGet && r.URL.Path == "/orgsync/_changes":
			lastSeq := "100"
			if r.URL.Query().Get("since") == "now" || r.URL.Query().Get("since") == lastSeq {
				return testJSONResponse(http.StatusOK, map[string]any{"last_seq": lastSeq, "results": []any{}}), nil
			}
			var results []struct {
				ID  string          `json:"id"`
				Seq string          `json:"seq"`
				Doc couchdb.FileDoc `json:"doc"`
			}
			for id, doc := range docs {
				results = append(results, struct {
					ID  string          `json:"id"`
					Seq string          `json:"seq"`
					Doc couchdb.FileDoc `json:"doc"`
				}{ID: id, Seq: "99", Doc: doc})
			}
			return testJSONResponse(http.StatusOK, map[string]any{"last_seq": lastSeq, "results": results}), nil
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/orgsync/file:"):
			id, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/orgsync/"))
			if err != nil {
				t.Fatal(err)
			}
			doc, ok := docs[id]
			if !ok {
				return testJSONResponse(http.StatusNotFound, map[string]string{"error": "not_found"}), nil
			}
			return testJSONResponse(http.StatusOK, doc), nil
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/orgsync/file:"):
			var doc couchdb.FileDoc
			if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
				t.Fatal(err)
			}
			doc.Rev = "2-updated"
			docs[doc.ID] = doc
			return testJSONResponse(http.StatusCreated, map[string]string{"ok": "true", "id": doc.ID, "rev": doc.Rev}), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
			return nil, nil
		}
	})
	client, err := couchdb.NewWithHTTPClient("http://couchdb.test", "orgsync", "", "", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func testJSONResponse(status int, value any) *http.Response {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		panic(err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body.Bytes())),
	}
}

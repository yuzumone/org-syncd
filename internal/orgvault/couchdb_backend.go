package orgvault

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yuzumone/org-syncd/internal/couchdb"
	"github.com/yuzumone/org-syncd/internal/files"
)

const backupDir = ".backup"

type CouchDBBackend struct {
	client   *couchdb.Client
	deviceID string
	timeout  time.Duration
}

func NewCouchDBBackend(client *couchdb.Client, deviceID string) *CouchDBBackend {
	if deviceID == "" {
		deviceID = "mcp"
	}
	return &CouchDBBackend{
		client:   client,
		deviceID: deviceID,
		timeout:  15 * time.Second,
	}
}

func (b *CouchDBBackend) ReadNote(notePath string) (Note, error) {
	rel, err := normalizeVaultPath(notePath)
	if err != nil {
		return Note{}, err
	}
	ctx, cancel := b.context()
	defer cancel()
	doc, ok, err := b.client.GetDoc(ctx, files.DocID(rel))
	if err != nil {
		return Note{}, err
	}
	if !ok || doc.Deleted {
		return Note{}, fmt.Errorf("note not found: %s", rel)
	}
	return noteFromDoc(doc), nil
}

func (b *CouchDBBackend) WriteNote(notePath, content string) (WriteResult, error) {
	if !utf8.ValidString(content) {
		return WriteResult{}, fmt.Errorf("content must be valid UTF-8")
	}
	rel, err := normalizeVaultPath(notePath)
	if err != nil {
		return WriteResult{}, err
	}
	now := time.Now()
	ctx, cancel := b.context()
	defer cancel()
	if err := b.client.EnsureDB(ctx); err != nil {
		return WriteResult{}, err
	}
	existing, ok, err := b.client.GetDoc(ctx, files.DocID(rel))
	if err != nil {
		return WriteResult{}, err
	}
	doc := b.doc(rel, content, now)
	created := !ok || existing.Deleted
	if ok {
		doc.Rev = existing.Rev
	}
	if _, err := b.client.PutDoc(ctx, doc); err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Path: rel, Created: created, ModifiedAt: now}, nil
}

func (b *CouchDBBackend) AppendNote(notePath, content string) (WriteResult, error) {
	if !utf8.ValidString(content) {
		return WriteResult{}, fmt.Errorf("content must be valid UTF-8")
	}
	rel, err := normalizeVaultPath(notePath)
	if err != nil {
		return WriteResult{}, err
	}
	result, err := b.appendOnce(rel, content)
	if err == nil || !couchdb.IsConflict(err) {
		return result, err
	}
	return b.appendOnce(rel, content)
}

func (b *CouchDBBackend) ListFolders() ([]Folder, error) {
	docs, err := b.fileDocs()
	if err != nil {
		return nil, err
	}
	counts := map[string]int{"": 0}
	for _, doc := range docs {
		dir := path.Dir(doc.Path)
		if dir == "." {
			dir = ""
		}
		counts[dir]++
		for parent := dir; parent != "" && parent != "."; parent = path.Dir(parent) {
			if _, ok := counts[parent]; !ok {
				counts[parent] = 0
			}
			if !strings.Contains(parent, "/") {
				break
			}
		}
	}
	out := make([]Folder, 0, len(counts))
	for folder, count := range counts {
		out = append(out, Folder{Path: folder, NoteCount: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (b *CouchDBBackend) ListNotes(opts ListOptions) ([]Note, error) {
	if opts.Sort == "" {
		opts.Sort = "name"
	}
	if opts.Order == "" {
		opts.Order = "asc"
	}
	if opts.Sort != "name" && opts.Sort != "modified" {
		return nil, fmt.Errorf("sort must be name or modified")
	}
	if opts.Order != "asc" && opts.Order != "desc" {
		return nil, fmt.Errorf("order must be asc or desc")
	}
	folder, err := normalizeOptionalFolder(opts.Folder)
	if err != nil {
		return nil, err
	}
	docs, err := b.fileDocs()
	if err != nil {
		return nil, err
	}
	var notes []Note
	for _, doc := range docs {
		if folder != "" && !strings.HasPrefix(doc.Path, folder+"/") {
			continue
		}
		if opts.Name != "" && !strings.Contains(strings.ToLower(path.Base(doc.Path)), strings.ToLower(opts.Name)) {
			continue
		}
		note := noteFromDoc(doc)
		if !opts.ModifiedAfter.IsZero() && !note.ModifiedAt.After(opts.ModifiedAfter) {
			continue
		}
		if opts.Tag != "" && !hasOrgTag(doc.Content, opts.Tag) {
			continue
		}
		notes = append(notes, note)
		notes[len(notes)-1].Content = ""
	}
	sortNotes(notes, opts)
	if opts.Limit > 0 && len(notes) > opts.Limit {
		notes = notes[:opts.Limit]
	}
	return notes, nil
}

func (b *CouchDBBackend) SearchNotes(opts SearchOptions) ([]Match, error) {
	if strings.TrimSpace(opts.Query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	folder, err := normalizeOptionalFolder(opts.Folder)
	if err != nil {
		return nil, err
	}
	docs, err := b.fileDocs()
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(opts.Query)
	var matches []Match
	for _, doc := range docs {
		if folder != "" && !strings.HasPrefix(doc.Path, folder+"/") {
			continue
		}
		lines := splitLines(doc.Content)
		title := parseTitle([]byte(doc.Content))
		for i, line := range lines {
			if !strings.Contains(strings.ToLower(line), needle) {
				continue
			}
			matches = append(matches, Match{
				Path:    doc.Path,
				Title:   title,
				Line:    i + 1,
				Text:    line,
				Context: contextLines(lines, i),
			})
			if opts.Limit > 0 && len(matches) >= opts.Limit {
				return matches, nil
			}
		}
	}
	return matches, nil
}

func (b *CouchDBBackend) appendOnce(rel, content string) (WriteResult, error) {
	now := time.Now()
	ctx, cancel := b.context()
	defer cancel()
	if err := b.client.EnsureDB(ctx); err != nil {
		return WriteResult{}, err
	}
	existing, ok, err := b.client.GetDoc(ctx, files.DocID(rel))
	if err != nil {
		return WriteResult{}, err
	}
	current := ""
	created := !ok || existing.Deleted
	if ok && !existing.Deleted {
		current = existing.Content
	}
	doc := b.doc(rel, current+content, now)
	if ok {
		doc.Rev = existing.Rev
	}
	if _, err := b.client.PutDoc(ctx, doc); err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Path: rel, Created: created, ModifiedAt: now}, nil
}

func (b *CouchDBBackend) fileDocs() ([]couchdb.FileDoc, error) {
	ctx, cancel := b.context()
	defer cancel()
	docs, err := b.client.AllDocs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]couchdb.FileDoc, 0, len(docs))
	for _, doc := range docs {
		if doc.Deleted || doc.Type != "file" {
			continue
		}
		rel, err := normalizeVaultPath(doc.Path)
		if err != nil || rel != doc.Path || !isOrgNote(doc.Path) {
			continue
		}
		out = append(out, doc)
	}
	return out, nil
}

func (b *CouchDBBackend) doc(rel, content string, now time.Time) couchdb.FileDoc {
	return couchdb.FileDoc{
		ID:            files.DocID(rel),
		Type:          "file",
		Path:          rel,
		Content:       content,
		ContentSHA256: files.SHA256Hex([]byte(content)),
		MTime:         now.Format(time.RFC3339),
		Deleted:       false,
		UpdatedBy:     b.deviceID,
	}
}

func (b *CouchDBBackend) context() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), b.timeout)
}

func noteFromDoc(doc couchdb.FileDoc) Note {
	mtime, _ := time.Parse(time.RFC3339, doc.MTime)
	if mtime.IsZero() {
		mtime = time.Now()
	}
	return Note{
		Path:       doc.Path,
		Name:       path.Base(doc.Path),
		Title:      parseTitle([]byte(doc.Content)),
		CreatedAt:  mtime,
		ModifiedAt: mtime,
		Size:       int64(len([]byte(doc.Content))),
		Content:    doc.Content,
	}
}

func normalizeOptionalFolder(folder string) (string, error) {
	if strings.TrimSpace(folder) == "" {
		return "", nil
	}
	return normalizeVaultPath(folder)
}

func normalizeVaultPath(notePath string) (string, error) {
	if notePath == "" {
		return "", fmt.Errorf("empty path")
	}
	notePath = filepath.ToSlash(notePath)
	if strings.Contains(notePath, "\x00") {
		return "", fmt.Errorf("path contains NUL byte")
	}
	if path.IsAbs(notePath) || strings.HasPrefix(notePath, "/") {
		return "", fmt.Errorf("absolute paths are not allowed: %s", notePath)
	}
	rel := path.Clean(notePath)
	if rel == "." || rel == "" {
		return "", fmt.Errorf("path must reference a note")
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("path traversal blocked: %s", notePath)
	}
	if rel == backupDir || strings.HasPrefix(rel, backupDir+"/") || files.IsHiddenPath(rel) {
		return "", fmt.Errorf("hidden and backup paths are not allowed: %s", rel)
	}
	return rel, nil
}

func sortNotes(notes []Note, opts ListOptions) {
	sort.Slice(notes, func(i, j int) bool {
		if opts.Sort == "modified" {
			if opts.Order == "desc" {
				return notes[i].ModifiedAt.After(notes[j].ModifiedAt)
			}
			return notes[i].ModifiedAt.Before(notes[j].ModifiedAt)
		}
		if opts.Order == "desc" {
			return notes[i].Path > notes[j].Path
		}
		return notes[i].Path < notes[j].Path
	})
}

func isOrgNote(rel string) bool {
	return strings.EqualFold(path.Ext(rel), ".org")
}

func parseTitle(data []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(strings.ToLower(line), "#+title:") {
			return strings.TrimSpace(line[len("#+title:"):])
		}
	}
	return ""
}

func hasOrgTag(content, tag string) bool {
	tag = strings.Trim(tag, ":")
	if tag == "" {
		return true
	}
	lower := strings.ToLower(content)
	needle := ":" + strings.ToLower(tag) + ":"
	if strings.Contains(lower, "#+filetags:") && strings.Contains(lower, needle) {
		return true
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.ToLower(line))
		if strings.HasPrefix(line, "*") && strings.HasSuffix(line, needle) {
			return true
		}
	}
	return false
}

func splitLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

func contextLines(lines []string, index int) []string {
	start := index - 1
	if start < 0 {
		start = 0
	}
	end := index + 2
	if end > len(lines) {
		end = len(lines)
	}
	return lines[start:end]
}

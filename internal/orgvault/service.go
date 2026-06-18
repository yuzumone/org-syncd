package orgvault

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yuzumone/org-syncd/internal/files"
)

const backupDir = ".backup"

type Service struct {
	root string
}

type Note struct {
	Path       string    `json:"path"`
	Name       string    `json:"name,omitempty"`
	Title      string    `json:"title,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	ModifiedAt time.Time `json:"modified_at"`
	Size       int64     `json:"size"`
	Content    string    `json:"content,omitempty"`
}

type Folder struct {
	Path      string `json:"path"`
	NoteCount int    `json:"note_count"`
}

type WriteResult struct {
	Path       string    `json:"path"`
	Created    bool      `json:"created"`
	BackupPath string    `json:"backup_path,omitempty"`
	ModifiedAt time.Time `json:"modified_at"`
}

type ListOptions struct {
	Folder        string
	Name          string
	Tag           string
	ModifiedAfter time.Time
	Sort          string
	Order         string
	Limit         int
}

type SearchOptions struct {
	Query  string
	Folder string
	Limit  int
}

type Match struct {
	Path    string   `json:"path"`
	Title   string   `json:"title,omitempty"`
	Line    int      `json:"line"`
	Text    string   `json:"text"`
	Context []string `json:"context"`
}

func New(root string) (*Service, error) {
	if root == "" {
		return nil, fmt.Errorf("ORG_ROOT is required")
	}
	expanded, err := expandHome(root)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return nil, err
	}
	return &Service{root: filepath.Clean(abs)}, nil
}

func DefaultRoot() string {
	if root := os.Getenv("ORG_ROOT"); root != "" {
		return root
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "org")
	}
	return "org"
}

func (s *Service) Root() string {
	return s.root
}

func (s *Service) ReadNote(path string) (Note, error) {
	rel, abs, err := s.safeNotePath(path, false)
	if err != nil {
		return Note{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Note{}, err
	}
	if !utf8.Valid(data) {
		return Note{}, fmt.Errorf("note is not valid UTF-8: %s", rel)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Note{}, err
	}
	return Note{
		Path:       rel,
		Content:    string(data),
		CreatedAt:  info.ModTime(),
		ModifiedAt: info.ModTime(),
		Size:       info.Size(),
	}, nil
}

func (s *Service) WriteNote(path, content string) (WriteResult, error) {
	return s.write(path, []byte(content), false)
}

func (s *Service) AppendNote(path, content string) (WriteResult, error) {
	return s.write(path, []byte(content), true)
}

func (s *Service) ListFolders() ([]Folder, error) {
	counts := map[string]int{"": 0}
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == s.root {
			return nil
		}
		rel, err := files.NormalizeRelativePath(s.root, path)
		if err != nil {
			return err
		}
		if shouldSkip(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			counts[rel] = 0
			return nil
		}
		if isOrgNote(rel) {
			dir := filepath.ToSlash(filepath.Dir(rel))
			if dir == "." {
				dir = ""
			}
			counts[dir]++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]Folder, 0, len(counts))
	for path, count := range counts {
		out = append(out, Folder{Path: path, NoteCount: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (s *Service) ListNotes(opts ListOptions) ([]Note, error) {
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
	base, err := s.safeFolder(opts.Folder)
	if err != nil {
		return nil, err
	}
	var notes []Note
	err = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == base {
			return nil
		}
		rel, err := files.NormalizeRelativePath(s.root, path)
		if err != nil {
			return err
		}
		if shouldSkip(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !isOrgNote(rel) {
			return nil
		}
		if opts.Name != "" && !strings.Contains(strings.ToLower(filepath.Base(rel)), strings.ToLower(opts.Name)) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !opts.ModifiedAfter.IsZero() && !info.ModTime().After(opts.ModifiedAfter) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !utf8.Valid(data) {
			return nil
		}
		if opts.Tag != "" && !hasOrgTag(string(data), opts.Tag) {
			return nil
		}
		notes = append(notes, Note{
			Path:       rel,
			Name:       filepath.Base(rel),
			Title:      parseTitle(data),
			CreatedAt:  info.ModTime(),
			ModifiedAt: info.ModTime(),
			Size:       info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
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
	if opts.Limit > 0 && len(notes) > opts.Limit {
		notes = notes[:opts.Limit]
	}
	return notes, nil
}

func (s *Service) SearchNotes(opts SearchOptions) ([]Match, error) {
	if strings.TrimSpace(opts.Query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	base, err := s.safeFolder(opts.Folder)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(opts.Query)
	var matches []Match
	err = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == base {
			return nil
		}
		rel, err := files.NormalizeRelativePath(s.root, path)
		if err != nil {
			return err
		}
		if shouldSkip(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !isOrgNote(rel) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !utf8.Valid(data) {
			return nil
		}
		lines := splitLines(string(data))
		title := parseTitle(data)
		for i, line := range lines {
			if !strings.Contains(strings.ToLower(line), needle) {
				continue
			}
			matches = append(matches, Match{
				Path:    rel,
				Title:   title,
				Line:    i + 1,
				Text:    line,
				Context: contextLines(lines, i),
			})
			if opts.Limit > 0 && len(matches) >= opts.Limit {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

func (s *Service) write(path string, content []byte, appendMode bool) (WriteResult, error) {
	if !utf8.Valid(content) {
		return WriteResult{}, fmt.Errorf("content must be valid UTF-8")
	}
	rel, abs, err := s.safeNotePath(path, true)
	if err != nil {
		return WriteResult{}, err
	}
	created := false
	existing, err := os.ReadFile(abs)
	if err != nil {
		if !os.IsNotExist(err) {
			return WriteResult{}, err
		}
		created = true
	} else if appendMode {
		content = append(existing, content...)
	}
	backupPath := ""
	if !created {
		backupPath, err = s.backup(rel, existing)
		if err != nil {
			return WriteResult{}, err
		}
	}
	if err := files.AtomicWrite(s.root, rel, content); err != nil {
		return WriteResult{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Path: rel, Created: created, BackupPath: backupPath, ModifiedAt: info.ModTime()}, nil
}

func (s *Service) backup(rel string, content []byte) (string, error) {
	stamp := time.Now().Format("20060102150405")
	for i := 0; ; i++ {
		dir := stamp
		if i > 0 {
			dir = fmt.Sprintf("%s-%02d", stamp, i)
		}
		backupRel := filepath.ToSlash(filepath.Join(backupDir, dir, filepath.FromSlash(rel)))
		backupAbs := filepath.Join(s.root, filepath.FromSlash(backupRel))
		if _, err := os.Stat(backupAbs); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(backupAbs), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(backupAbs, content, 0o644); err != nil {
			return "", err
		}
		return backupRel, nil
	}
}

func (s *Service) safeNotePath(path string, allowMissing bool) (string, string, error) {
	rel, err := files.NormalizeRelativePath(s.root, path)
	if err != nil {
		return "", "", err
	}
	if rel == backupDir || strings.HasPrefix(rel, backupDir+"/") || files.IsHiddenPath(rel) {
		return "", "", fmt.Errorf("hidden and backup paths are not allowed: %s", rel)
	}
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	if !strings.HasPrefix(abs, s.root+string(filepath.Separator)) && abs != s.root {
		return "", "", fmt.Errorf("path escapes ORG_ROOT: %s", path)
	}
	if err := s.ensureNoSymlinkComponents(rel, allowMissing); err != nil {
		return "", "", err
	}
	if !allowMissing {
		info, err := os.Lstat(abs)
		if err != nil {
			return "", "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("symlinks are not allowed: %s", rel)
		}
	}
	return rel, abs, nil
}

func (s *Service) ensureNoSymlinkComponents(rel string, allowMissing bool) error {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	current := s.root
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if allowMissing && os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed: %s", strings.Join(parts[:i+1], "/"))
		}
	}
	return nil
}

func (s *Service) safeFolder(folder string) (string, error) {
	if folder == "" {
		return s.root, nil
	}
	rel, err := files.NormalizeRelativePath(s.root, folder)
	if err != nil {
		return "", err
	}
	if rel == backupDir || strings.HasPrefix(rel, backupDir+"/") || files.IsHiddenPath(rel) {
		return "", fmt.Errorf("hidden and backup folders are not allowed: %s", rel)
	}
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	info, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("folder is not a directory: %s", rel)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("symlink folders are not allowed: %s", rel)
	}
	return abs, nil
}

func shouldSkip(rel string, d fs.DirEntry) bool {
	if rel == backupDir || strings.HasPrefix(rel, backupDir+"/") || files.IsHiddenPath(rel) {
		return true
	}
	return d.Type()&os.ModeSymlink != 0
}

func isOrgNote(rel string) bool {
	return strings.EqualFold(filepath.Ext(rel), ".org")
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

func expandHome(path string) (string, error) {
	if path == "~" {
		return os.UserHomeDir()
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

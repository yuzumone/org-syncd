package watcher

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/yuzumone/org-syncd/internal/files"
)

type Watcher struct {
	localDir string
	matcher  files.Matcher
	debounce time.Duration
	log      *slog.Logger
}

func New(localDir string, matcher files.Matcher, debounce time.Duration, log *slog.Logger) Watcher {
	return Watcher{localDir: localDir, matcher: matcher, debounce: debounce, log: log}
}

func (w Watcher) Run(ctx context.Context, changed chan<- struct{}) error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() {
		if err := fsw.Close(); err != nil {
			w.log.Warn("failed to close watcher", "error", err)
		}
	}()

	if err := w.addDirs(fsw); err != nil {
		return err
	}

	timer := time.NewTimer(w.debounce)
	if !timer.Stop() {
		<-timer.C
	}
	pending := false

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-fsw.Errors:
			if err != nil {
				w.log.Warn("watch error", "error", err)
			}
		case event := <-fsw.Events:
			if event.Name == "" {
				continue
			}
			if event.Has(fsnotify.Create) {
				w.addCreatedDir(fsw, event.Name)
			}
			if !w.shouldTrigger(event.Name) {
				continue
			}
			pending = true
			resetTimer(timer, w.debounce)
		case <-timer.C:
			if pending {
				pending = false
				select {
				case changed <- struct{}{}:
				default:
				}
			}
		}
	}
}

func (w Watcher) addDirs(fsw *fsnotify.Watcher) error {
	return filepath.WalkDir(w.localDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == w.localDir {
			return fsw.Add(path)
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := files.NormalizeRelativePath(w.localDir, path)
		if err != nil {
			return err
		}
		if files.IsHiddenPath(rel) || w.matcher.Ignored(rel) {
			return filepath.SkipDir
		}
		return fsw.Add(path)
	})
}

func (w Watcher) addCreatedDir(fsw *fsnotify.Watcher, path string) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	rel, err := files.NormalizeRelativePath(w.localDir, path)
	if err != nil {
		return
	}
	if files.IsHiddenPath(rel) || w.matcher.Ignored(rel) {
		return
	}
	if err := fsw.Add(path); err != nil {
		w.log.Warn("watch add failed", "path", rel, "error", err)
	}
}

func (w Watcher) shouldTrigger(path string) bool {
	rel, err := files.NormalizeRelativePath(w.localDir, path)
	if err != nil {
		return false
	}
	return w.matcher.Include(rel)
}

func resetTimer(timer *time.Timer, d time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(d)
}

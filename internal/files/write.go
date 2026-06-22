package files

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func AtomicWrite(localDir, relPath string, content []byte) error {
	rel, err := NormalizeRelativePath(localDir, relPath)
	if err != nil {
		return err
	}
	target := filepath.Join(localDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(target), ".org-syncd-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}

func ConflictPath(relPath string, t time.Time) string {
	ext := filepath.Ext(relPath)
	base := relPath[:len(relPath)-len(ext)]
	return fmt.Sprintf("%s.conflict-%s%s", base, t.Format("20060102-150405"), ext)
}

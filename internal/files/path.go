package files

import (
	"fmt"
	"path/filepath"
	"strings"
)

func NormalizeRelativePath(localDir, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}

	var rel string
	if filepath.IsAbs(path) {
		r, err := filepath.Rel(localDir, path)
		if err != nil {
			return "", err
		}
		rel = r
	} else {
		rel = path
	}

	rel = filepath.Clean(rel)
	if rel == "." || rel == "" {
		return "", fmt.Errorf("path must reference a file")
	}
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes local_dir: %s", path)
	}
	if strings.Contains(rel, "\x00") {
		return "", fmt.Errorf("path contains NUL byte")
	}
	return filepath.ToSlash(rel), nil
}

func DocID(relPath string) string {
	return "file:" + filepath.ToSlash(relPath)
}

func IsHiddenPath(relPath string) bool {
	for _, part := range strings.Split(filepath.ToSlash(relPath), "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

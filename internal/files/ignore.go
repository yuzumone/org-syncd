package files

import (
	"path/filepath"
	"strings"
)

type Matcher struct {
	includeExts map[string]bool
	ignore      []string
}

func NewMatcher(includeExts, ignore []string) Matcher {
	exts := make(map[string]bool, len(includeExts))
	for _, ext := range includeExts {
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		exts[strings.ToLower(ext)] = true
	}
	return Matcher{includeExts: exts, ignore: ignore}
}

func (m Matcher) Include(relPath string) bool {
	relPath = filepath.ToSlash(filepath.Clean(relPath))
	if IsHiddenPath(relPath) && !m.explicitlyIncluded(relPath) {
		return false
	}
	if m.Ignored(relPath) {
		return false
	}
	return m.includeExts[strings.ToLower(filepath.Ext(relPath))]
}

func (m Matcher) Ignored(relPath string) bool {
	relPath = filepath.ToSlash(filepath.Clean(relPath))
	base := filepath.Base(relPath)
	for _, pattern := range m.ignore {
		pattern = filepath.ToSlash(filepath.Clean(pattern))
		if pattern == "." || pattern == "" {
			continue
		}
		if pattern == relPath || pattern == base {
			return true
		}
		if ok, _ := filepath.Match(pattern, base); ok {
			return true
		}
		if ok, _ := filepath.Match(pattern, relPath); ok {
			return true
		}
	}
	return false
}

func (m Matcher) explicitlyIncluded(relPath string) bool {
	base := filepath.Base(relPath)
	for _, pattern := range m.ignore {
		if pattern == base || pattern == relPath {
			return false
		}
	}
	return false
}

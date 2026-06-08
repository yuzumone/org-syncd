package files

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type LocalFile struct {
	Path          string
	AbsolutePath  string
	Content       []byte
	ContentSHA256 string
	MTime         time.Time
}

func Scan(localDir string, matcher Matcher) ([]LocalFile, error) {
	var out []LocalFile
	err := filepath.WalkDir(localDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == localDir {
			return nil
		}

		rel, err := NormalizeRelativePath(localDir, path)
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if matcher.Ignored(rel) || IsHiddenPath(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !matcher.Include(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, LocalFile{
			Path:          rel,
			AbsolutePath:  path,
			Content:       data,
			ContentSHA256: SHA256Hex(data),
			MTime:         info.ModTime(),
		})
		return nil
	})
	return out, err
}

func MapByPath(files []LocalFile) map[string]LocalFile {
	m := make(map[string]LocalFile, len(files))
	for _, f := range files {
		m[f.Path] = f
	}
	return m
}

// Package scanner recursively discovers env files beneath a root directory.
// It matches file names against configurable glob patterns, skips noisy
// directories such as node_modules, and computes the backup-relative path each
// file should be mirrored to.
package scanner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Found describes a single discovered env file.
type Found struct {
	// Source is the absolute path of the file on disk.
	Source string

	// Backup is the path, relative to the repository root, where the file
	// should be mirrored (e.g. "my-project/.env").
	Backup string
}

// Scanner walks a directory tree looking for env files.
type Scanner struct {
	root       string
	ignoreDirs map[string]struct{}
	patterns   []string
}

// New constructs a Scanner. root is the directory to scan, ignoreDirs are
// directory base names to skip, and patterns are filepath.Match globs tested
// against each file's base name.
func New(root string, ignoreDirs, patterns []string) *Scanner {
	ignore := make(map[string]struct{}, len(ignoreDirs))
	for _, d := range ignoreDirs {
		ignore[d] = struct{}{}
	}
	return &Scanner{
		root:       root,
		ignoreDirs: ignore,
		patterns:   patterns,
	}
}

// Scan walks the tree and returns every matching env file. Unreadable
// subdirectories are skipped rather than aborting the whole scan, so a single
// permission error cannot prevent the rest of the tree from being backed up.
func (s *Scanner) Scan() ([]Found, error) {
	var found []Found

	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip directories we cannot read; keep scanning the rest.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			if _, skip := s.ignoreDirs[d.Name()]; skip {
				return fs.SkipDir
			}
			return nil
		}

		// Only consider regular files; ignore symlinks and special files so we
		// never follow a link out of the tree or hash a device node.
		if !d.Type().IsRegular() {
			return nil
		}

		if !s.matches(d.Name()) {
			return nil
		}

		rel, relErr := filepath.Rel(s.root, path)
		if relErr != nil {
			return nil
		}

		found = append(found, Found{
			Source: path,
			Backup: filepath.ToSlash(rel),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan %q: %w", s.root, err)
	}
	return found, nil
}

// matches reports whether name satisfies any configured pattern. An invalid
// pattern is treated as non-matching rather than an error so a typo in config
// cannot crash the daemon.
func (s *Scanner) matches(name string) bool {
	for _, p := range s.patterns {
		ok, err := filepath.Match(p, name)
		if err == nil && ok {
			return true
		}
	}
	return false
}

// HashFile returns the hex-encoded SHA-256 hash of the file at path. It streams
// the file so large files do not need to be held in memory.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %q: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

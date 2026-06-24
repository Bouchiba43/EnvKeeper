// Package manifest provides a small, concurrency-safe JSON store that tracks
// the state of every env file under management: its source path, the mirrored
// backup path, its current SHA-256 hash, whether it is awaiting a sync, and
// when it was last synced.
//
// The manifest is the daemon's source of truth across restarts: it lets the
// tool know which files were previously tracked, detect content changes by
// comparing hashes, and decide whether a sync is required.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Entry records the tracked state of a single env file. It intentionally
// stores only metadata — never the file's contents — so secrets are never
// persisted outside the backup repository itself.
type Entry struct {
	// Source is the absolute path of the original file on disk.
	Source string `json:"source"`

	// Backup is the path relative to the repository root where the file is
	// mirrored (e.g. "my-project/.env").
	Backup string `json:"backup"`

	// SHA256 is the hex-encoded SHA-256 hash of the file contents.
	SHA256 string `json:"sha256"`

	// Dirty indicates the mirrored copy has changed since the last successful
	// git sync and therefore needs to be committed.
	Dirty bool `json:"dirty"`

	// LastSync is the timestamp of the last successful sync of this file.
	LastSync time.Time `json:"last_sync"`
}

// Manifest is an in-memory map of backup-relative path -> Entry, backed by a
// JSON file on disk. All exported methods are safe for concurrent use.
type Manifest struct {
	path string

	mu      sync.RWMutex
	entries map[string]*Entry // keyed by Entry.Backup
}

// Load reads the manifest from path. A missing file yields an empty manifest
// so the daemon can start fresh on first run.
func Load(path string) (*Manifest, error) {
	m := &Manifest{
		path:    path,
		entries: make(map[string]*Entry),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, fmt.Errorf("read manifest %q: %w", path, err)
	}

	if len(data) == 0 {
		return m, nil
	}

	var entries []*Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse manifest %q: %w", path, err)
	}
	for _, e := range entries {
		if e.Backup == "" {
			continue
		}
		m.entries[e.Backup] = e
	}
	return m, nil
}

// Save atomically writes the manifest to disk. It writes to a temporary file
// and renames it into place so a crash mid-write cannot corrupt the manifest.
func (m *Manifest) Save() error {
	m.mu.RLock()
	entries := make([]*Entry, 0, len(m.entries))
	for _, e := range m.entries {
		entries = append(entries, e)
	}
	m.mu.RUnlock()

	// Deterministic ordering keeps diffs of the manifest readable.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Backup < entries[j].Backup
	})

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return fmt.Errorf("create manifest dir: %w", err)
	}

	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write manifest temp: %w", err)
	}
	if err := os.Rename(tmp, m.path); err != nil {
		return fmt.Errorf("commit manifest: %w", err)
	}
	return nil
}

// Get returns a copy of the entry for the given backup path, if tracked.
func (m *Manifest) Get(backup string) (Entry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[backup]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// GetBySource returns a copy of the entry whose source path matches, if any.
func (m *Manifest) GetBySource(source string) (Entry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.entries {
		if e.Source == source {
			return *e, true
		}
	}
	return Entry{}, false
}

// Upsert inserts or updates an entry, returning true if the content hash
// changed (or the entry is new). When the hash changes the entry is marked
// dirty so the next sync will pick it up.
func (m *Manifest) Upsert(source, backup, sha256 string) (changed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.entries[backup]
	if !ok {
		m.entries[backup] = &Entry{
			Source: source,
			Backup: backup,
			SHA256: sha256,
			Dirty:  true,
		}
		return true
	}

	// Keep the source path current (handles a project being moved/renamed
	// while keeping the same backup location).
	e.Source = source

	if e.SHA256 != sha256 {
		e.SHA256 = sha256
		e.Dirty = true
		return true
	}
	return false
}

// Remove deletes the entry for the given backup path. It returns the removed
// entry so callers can clean up the mirrored file.
func (m *Manifest) Remove(backup string) (Entry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[backup]
	if !ok {
		return Entry{}, false
	}
	delete(m.entries, backup)
	return *e, true
}

// MarkDirty flags the entry at backup as needing a sync.
func (m *Manifest) MarkDirty(backup string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[backup]; ok {
		e.Dirty = true
	}
}

// HasDirty reports whether any tracked file is awaiting a sync.
func (m *Manifest) HasDirty() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.entries {
		if e.Dirty {
			return true
		}
	}
	return false
}

// MarkAllSynced clears the dirty flag on every entry and stamps LastSync.
// It is called after a successful git push.
func (m *Manifest) MarkAllSynced(t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.entries {
		if e.Dirty {
			e.LastSync = t
			e.Dirty = false
		}
	}
}

// Entries returns a snapshot copy of all entries, sorted by backup path.
func (m *Manifest) Entries() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Entry, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Backup < out[j].Backup
	})
	return out
}

// Sources returns the set of currently tracked source paths. It is used by the
// scanner to detect files that have disappeared from disk.
func (m *Manifest) Sources() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]string, len(m.entries))
	for _, e := range m.entries {
		out[e.Source] = e.Backup
	}
	return out
}

// Len returns the number of tracked files.
func (m *Manifest) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

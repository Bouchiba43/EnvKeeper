package manifest

import (
	"path/filepath"
	"testing"
	"time"
)

func TestUpsertDetectsChanges(t *testing.T) {
	m := &Manifest{entries: make(map[string]*Entry)}

	if changed := m.Upsert("/src/.env", "proj/.env", "hash1"); !changed {
		t.Fatal("new entry should report changed=true")
	}
	if changed := m.Upsert("/src/.env", "proj/.env", "hash1"); changed {
		t.Fatal("identical hash should report changed=false")
	}
	if changed := m.Upsert("/src/.env", "proj/.env", "hash2"); !changed {
		t.Fatal("new hash should report changed=true")
	}
	if !m.HasDirty() {
		t.Fatal("changed entry should be dirty")
	}
}

func TestMarkAllSyncedClearsDirty(t *testing.T) {
	m := &Manifest{entries: make(map[string]*Entry)}
	m.Upsert("/src/.env", "proj/.env", "hash1")

	now := time.Now()
	m.MarkAllSynced(now)

	if m.HasDirty() {
		t.Fatal("entries should be clean after MarkAllSynced")
	}
	e, ok := m.Get("proj/.env")
	if !ok || !e.LastSync.Equal(now) {
		t.Fatalf("LastSync not stamped: %+v", e)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	m := &Manifest{path: path, entries: make(map[string]*Entry)}
	m.Upsert("/src/.env", "proj/.env", "hash1")
	if err := m.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", loaded.Len())
	}
	if _, ok := loaded.Get("proj/.env"); !ok {
		t.Fatal("entry not round-tripped")
	}
}

func TestRemove(t *testing.T) {
	m := &Manifest{entries: make(map[string]*Entry)}
	m.Upsert("/src/.env", "proj/.env", "hash1")
	if _, ok := m.Remove("proj/.env"); !ok {
		t.Fatal("remove should report existing entry")
	}
	if m.Len() != 0 {
		t.Fatal("entry should be gone")
	}
}

func TestLoadMissingFile(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if m.Len() != 0 {
		t.Fatal("missing file should yield empty manifest")
	}
}

package watcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherDetectsWrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, ".env")
	if err := os.WriteFile(src, []byte("A=1"), 0o600); err != nil {
		t.Fatal(err)
	}

	w, err := New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := w.Add(src); err != nil {
		t.Fatalf("add: %v", err)
	}
	if w.Count() != 1 {
		t.Fatalf("expected 1 watched file, got %d", w.Count())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	// Give the watcher a moment to start before mutating the file.
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(src, []byte("A=2"), 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-w.Events():
		if ev.Source != src {
			t.Fatalf("unexpected source: %s", ev.Source)
		}
		if ev.Op != OpModified {
			t.Fatalf("expected OpModified, got %v", ev.Op)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for write event")
	}
}

func TestRemoveDropsTracking(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, ".env")
	if err := os.WriteFile(src, []byte("A=1"), 0o600); err != nil {
		t.Fatal(err)
	}

	w, err := New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer w.fsw.Close()

	if err := w.Add(src); err != nil {
		t.Fatalf("add: %v", err)
	}
	w.Remove(src)
	if w.Count() != 0 {
		t.Fatalf("expected 0 watched files, got %d", w.Count())
	}
}

package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// configureIdentity sets a local commit identity so commits succeed in CI
// environments without a global git config.
func configureIdentity(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func TestSyncCommitsOnlyWhenChanged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	repo := New(dir, "main", "origin")
	ctx := context.Background()

	if err := repo.EnsureInit(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	configureIdentity(t, dir)

	// No changes yet -> no commit.
	committed, err := repo.Sync(ctx, "initial")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if committed {
		t.Fatal("expected no commit on empty repo")
	}

	// Add a mirrored file -> commit (no remote, so local commit only).
	if err := os.WriteFile(filepath.Join(dir, "proj", ".env"), []byte("A=1"), 0o600); err != nil {
		// proj dir does not exist yet
		if err := os.MkdirAll(filepath.Join(dir, "proj"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "proj", ".env"), []byte("A=1"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	committed, err = repo.Sync(ctx, "add env")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !committed {
		t.Fatal("expected a commit after adding a file")
	}

	// Nothing changed since last commit -> no commit.
	committed, err = repo.Sync(ctx, "noop")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if committed {
		t.Fatal("expected no commit when tree is clean")
	}
}

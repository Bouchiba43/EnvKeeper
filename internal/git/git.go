// Package git wraps the system `git` binary to manage the backup repository.
//
// Shelling out to git (rather than using a Go git library) keeps dependencies
// minimal and guarantees identical behaviour to the user's own git setup —
// credentials, SSH keys, signing config and remotes all work exactly as they
// do on the command line.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo represents a local git repository at Path.
type Repo struct {
	// Path is the absolute path to the repository working tree.
	Path string

	// Branch is the branch commits are made on and pushed to.
	Branch string

	// Remote is the remote name pushed to (e.g. "origin").
	Remote string
}

// New returns a Repo handle. It does not touch the filesystem; call EnsureInit
// to create/initialise the repository.
func New(path, branch, remote string) *Repo {
	return &Repo{Path: path, Branch: branch, Remote: remote}
}

// EnsureInit makes sure Path exists and is a git repository on the configured
// branch. It is safe to call repeatedly: an already-initialised repo is left
// untouched apart from ensuring the branch exists.
func (r *Repo) EnsureInit(ctx context.Context) error {
	if err := os.MkdirAll(r.Path, 0o755); err != nil {
		return fmt.Errorf("create repo dir: %w", err)
	}

	if !r.isRepo(ctx) {
		if _, err := r.run(ctx, "init"); err != nil {
			return fmt.Errorf("git init: %w", err)
		}
	}

	// Ensure we are on the configured branch. `git checkout -B` creates it if
	// missing and switches to it otherwise, which is idempotent for our needs.
	if _, err := r.run(ctx, "checkout", "-B", r.Branch); err != nil {
		return fmt.Errorf("checkout branch %q: %w", r.Branch, err)
	}
	return nil
}

// isRepo reports whether Path is inside a git working tree.
func (r *Repo) isRepo(ctx context.Context) bool {
	if _, err := os.Stat(filepath.Join(r.Path, ".git")); err == nil {
		return true
	}
	out, err := r.run(ctx, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// HasChanges reports whether the working tree has any staged or unstaged
// changes (including untracked files) relative to HEAD.
func (r *Repo) HasChanges(ctx context.Context) (bool, error) {
	out, err := r.run(ctx, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return strings.TrimSpace(out) != "", nil
}

// Sync stages all changes, commits them with the given message, and pushes to
// the configured remote/branch. If there is nothing to commit it returns
// (false, nil) without creating an empty commit. A push to a repository with
// no configured remote is treated as a non-fatal local-only commit.
func (r *Repo) Sync(ctx context.Context, message string) (committed bool, err error) {
	if _, err := r.run(ctx, "add", "--all"); err != nil {
		return false, fmt.Errorf("git add: %w", err)
	}

	changes, err := r.HasChanges(ctx)
	if err != nil {
		return false, err
	}
	if !changes {
		return false, nil
	}

	if _, err := r.run(ctx, "commit", "-m", message); err != nil {
		return false, fmt.Errorf("git commit: %w", err)
	}

	if !r.hasRemote(ctx) {
		// No remote configured: a local commit is still a successful backup.
		return true, nil
	}

	if _, err := r.run(ctx, "push", r.Remote, r.Branch); err != nil {
		// The commit succeeded; surface the push failure but report that work
		// was committed so the caller can decide how to handle a retry.
		return true, fmt.Errorf("git push: %w", err)
	}
	return true, nil
}

// hasRemote reports whether the configured remote exists.
func (r *Repo) hasRemote(ctx context.Context) bool {
	out, err := r.run(ctx, "remote")
	if err != nil {
		return false
	}
	for _, name := range strings.Fields(out) {
		if name == r.Remote {
			return true
		}
	}
	return false
}

// run executes a git command in the repository directory and returns its
// combined trimmed stdout. Stderr is included in the error for diagnostics.
//
// Note: git output for env-sync only ever contains file paths and status
// codes, never file contents, so logging it does not leak secrets.
func (r *Repo) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Path

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("%w: %s", err, msg)
	}
	return stdout.String(), nil
}

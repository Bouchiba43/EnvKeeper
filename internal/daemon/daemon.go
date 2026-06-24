// Package daemon orchestrates the scanner, manifest, watcher and git packages
// into the running env-sync service. It is the component that decides *when*
// files are mirrored and *when* the backup repository is committed and pushed.
//
// Design principles:
//   - The git repository is the source of truth for "is a sync needed": every
//     scheduled sync asks git whether the working tree changed, so a missed
//     event or restart can never silently skip a backup.
//   - Source-file deletions never delete the mirrored backup. The whole point
//     of the tool is to survive losing local files, so a deleted .env keeps its
//     last backed-up copy in the repository.
package daemon

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/bouchiba/env-sync/internal/config"
	"github.com/bouchiba/env-sync/internal/git"
	"github.com/bouchiba/env-sync/internal/manifest"
	"github.com/bouchiba/env-sync/internal/scanner"
	"github.com/bouchiba/env-sync/internal/watcher"
	"github.com/bouchiba/env-sync/pkg/logx"
)

// Daemon is the env-sync service engine.
type Daemon struct {
	cfg *config.Config
	log *logx.Logger
	man *manifest.Manifest
	scn *scanner.Scanner
	repo *git.Repo
}

// New constructs a Daemon: it loads the manifest and prepares the scanner and
// git handles. It does not perform any scanning, watching or git
// initialisation — call Reconcile, SyncGit or Run for that.
func New(cfg *config.Config, log *logx.Logger) (*Daemon, error) {
	man, err := manifest.Load(cfg.ManifestPath)
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}
	return &Daemon{
		cfg: cfg,
		log: log,
		man: man,
		scn: scanner.New(cfg.ScanRoot, cfg.IgnoreDirs, cfg.FilePatterns),
		repo: git.New(cfg.RepoPath, cfg.GitBranch, cfg.GitRemote),
	}, nil
}

// Manifest exposes the underlying manifest (used by the status command).
func (d *Daemon) Manifest() *manifest.Manifest { return d.man }

// Reconcile performs a full scan: it mirrors every discovered env file into the
// backup repository, updates the manifest, untracks files that have vanished,
// and (when a watcher is supplied) keeps the watch set in sync. It is safe to
// call repeatedly and is the mechanism by which brand-new projects are
// discovered.
func (d *Daemon) Reconcile(ctx context.Context, w *watcher.Watcher) error {
	found, err := d.scn.Scan()
	if err != nil {
		return err
	}

	present := make(map[string]struct{}, len(found))
	for _, f := range found {
		present[f.Source] = struct{}{}

		changed, err := d.mirror(f)
		if err != nil {
			d.log.Warnf("could not mirror %s: %v", f.Backup, err)
			continue
		}
		if w != nil {
			if err := w.Add(f.Source); err != nil {
				d.log.Warnf("could not watch %s: %v", f.Backup, err)
			}
		}
		if changed {
			d.log.Infof("Tracking %s", f.Backup)
		}
	}

	// Untrack files that no longer exist on disk. The mirrored copy is kept on
	// purpose so an accidental deletion does not lose the backup.
	for source, backup := range d.man.Sources() {
		if _, ok := present[source]; ok {
			continue
		}
		d.man.Remove(backup)
		if w != nil {
			w.Remove(source)
		}
		d.log.Infof("Stopped tracking removed file: %s (backup retained)", backup)
	}

	if err := d.man.Save(); err != nil {
		return fmt.Errorf("save manifest: %w", err)
	}
	return nil
}

// mirror copies a discovered file into the backup repository if its contents
// have changed (or the mirror is missing) and updates the manifest. It returns
// whether the file's tracked hash changed.
func (d *Daemon) mirror(f scanner.Found) (bool, error) {
	hash, err := scanner.HashFile(f.Source)
	if err != nil {
		return false, err
	}

	dest := filepath.Join(d.cfg.RepoPath, filepath.FromSlash(f.Backup))

	existing, ok := d.man.GetBySource(f.Source)
	mirrorExists := fileExists(dest)
	if ok && existing.SHA256 == hash && mirrorExists {
		// Already mirrored and unchanged: nothing to copy.
		return false, nil
	}

	if err := copyFile(f.Source, dest); err != nil {
		return false, err
	}
	return d.man.Upsert(f.Source, f.Backup, hash), nil
}

// HandleEvent reacts to a single filesystem event from the watcher.
func (d *Daemon) HandleEvent(w *watcher.Watcher, ev watcher.Event) {
	switch ev.Op {
	case watcher.OpModified:
		d.onModified(ev.Source)
	case watcher.OpRemoved:
		// A "remove" is often an editor's atomic save (write temp, rename over
		// the original). If the file is still present, treat it as a change.
		if fileExists(ev.Source) {
			d.onModified(ev.Source)
			return
		}
		d.onRemoved(w, ev.Source)
	}
	if err := d.man.Save(); err != nil {
		d.log.Warnf("could not save manifest: %v", err)
	}
}

func (d *Daemon) onModified(source string) {
	entry, ok := d.man.GetBySource(source)
	if !ok {
		// Not yet tracked (e.g. a new sibling file); the next rescan picks it up.
		return
	}
	changed, err := d.mirror(scanner.Found{Source: source, Backup: entry.Backup})
	if err != nil {
		d.log.Warnf("could not mirror %s: %v", entry.Backup, err)
		return
	}
	if changed {
		d.log.Infof("Detected modification: %s", entry.Backup)
		d.log.Infof("Scheduled for next sync.")
	}
}

func (d *Daemon) onRemoved(w *watcher.Watcher, source string) {
	entry, ok := d.man.GetBySource(source)
	if !ok {
		return
	}
	d.man.Remove(entry.Backup)
	w.Remove(source)
	d.log.Infof("Stopped tracking deleted file: %s (backup retained)", entry.Backup)
}

// SyncGit commits and pushes any pending changes. It is a no-op when the backup
// repository's working tree is clean. The commit message embeds a UTC
// timestamp.
func (d *Daemon) SyncGit(ctx context.Context) error {
	if err := d.repo.EnsureInit(ctx); err != nil {
		return fmt.Errorf("init repo: %w", err)
	}

	changes, err := d.repo.HasChanges(ctx)
	if err != nil {
		return err
	}
	if !changes {
		d.log.Infof("No changes.")
		return nil
	}

	msg := fmt.Sprintf("Sync env files - %s", time.Now().UTC().Format("2006-01-02 15:04 UTC"))
	committed, err := d.repo.Sync(ctx, msg)
	if err != nil {
		return fmt.Errorf("git sync: %w", err)
	}
	if committed {
		now := time.Now().UTC()
		d.man.MarkAllSynced(now)
		if err := d.man.Save(); err != nil {
			d.log.Warnf("could not save manifest after sync: %v", err)
		}
		d.log.Infof("Committed and pushed: %s", msg)
	} else {
		d.log.Infof("No changes.")
	}
	return nil
}

// Run starts the long-running daemon: it initialises the repo, performs an
// initial scan, watches tracked files, and on a schedule rescans for new
// projects and pushes pending changes. It returns when ctx is cancelled,
// performing a best-effort final sync on the way out.
func (d *Daemon) Run(ctx context.Context) error {
	if err := d.repo.EnsureInit(ctx); err != nil {
		return fmt.Errorf("init repo: %w", err)
	}

	w, err := watcher.New()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}

	watchErr := make(chan error, 1)
	go func() { watchErr <- w.Run(ctx) }()

	d.log.Infof("Scanning projects...")
	if err := d.Reconcile(ctx, w); err != nil {
		d.log.Errorf("initial scan failed: %v", err)
	}
	d.log.Infof("Watching %d env files...", w.Count())

	syncTicker := time.NewTicker(d.cfg.SyncInterval)
	rescanTicker := time.NewTicker(d.cfg.RescanInterval)
	defer syncTicker.Stop()
	defer rescanTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.log.Infof("Shutting down, running final sync...")
			// Use a detached context so the final sync can complete even though
			// the run context is already cancelled.
			final, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			if err := d.SyncGit(final); err != nil {
				d.log.Warnf("final sync failed: %v", err)
			}
			cancel()
			<-watchErr
			return nil

		case ev, ok := <-w.Events():
			if !ok {
				continue
			}
			d.HandleEvent(w, ev)

		case <-rescanTicker.C:
			d.log.Infof("Rescanning projects...")
			if err := d.Reconcile(ctx, w); err != nil {
				d.log.Warnf("rescan failed: %v", err)
			}
			d.log.Infof("Watching %d env files...", w.Count())

		case <-syncTicker.C:
			d.log.Infof("Running scheduled git sync...")
			if err := d.SyncGit(ctx); err != nil {
				d.log.Warnf("scheduled sync failed: %v", err)
			}
		}
	}
}

// fileExists reports whether path exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// copyFile copies src to dst, creating parent directories as needed. The
// destination is written with 0600 permissions since it holds secrets, and the
// write is atomic (temp file + rename) so a crash cannot leave a partial
// mirror.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("copy: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close dest: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("commit dest: %w", err)
	}
	return nil
}

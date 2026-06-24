// Package watcher wraps fsnotify (inotify on Linux) to observe tracked env
// files for modifications, deletions and renames.
//
// To keep the watch set small and robust, the watcher watches the *parent
// directories* of tracked files rather than each individual file. Editors
// frequently save by writing a temporary file and renaming it over the
// original, which removes the inotify watch on the file itself; watching the
// directory survives that pattern and still reports the change.
package watcher

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Op describes the kind of change observed for a file.
type Op int

const (
	// OpModified means the file was created or its contents changed.
	OpModified Op = iota
	// OpRemoved means the file was deleted or renamed away.
	OpRemoved
)

// Event reports a change to a watched source file.
type Event struct {
	// Source is the absolute path of the file that changed.
	Source string
	// Op is the kind of change.
	Op Op
}

// Watcher tracks a set of source files and emits an Event whenever one of them
// changes. It is safe for concurrent use.
type Watcher struct {
	fsw    *fsnotify.Watcher
	events chan Event

	mu       sync.Mutex
	tracked  map[string]struct{} // absolute source file paths
	dirCount map[string]int      // parent dir -> number of tracked files in it
}

// New creates a Watcher backed by a fresh fsnotify watcher.
func New() (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		fsw:      fsw,
		events:   make(chan Event, 128),
		tracked:  make(map[string]struct{}),
		dirCount: make(map[string]int),
	}, nil
}

// Events returns the channel on which change events are delivered. The channel
// is closed when Run returns.
func (w *Watcher) Events() <-chan Event {
	return w.events
}

// Add begins tracking the given absolute source path. Adding a file already
// tracked is a no-op. The parent directory is watched; reference counting
// ensures the directory watch persists until the last file in it is removed.
func (w *Watcher) Add(source string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, ok := w.tracked[source]; ok {
		return nil
	}

	dir := filepath.Dir(source)
	if w.dirCount[dir] == 0 {
		if err := w.fsw.Add(dir); err != nil {
			return err
		}
	}
	w.tracked[source] = struct{}{}
	w.dirCount[dir]++
	return nil
}

// Remove stops tracking the given source path. When the last tracked file in a
// directory is removed, the directory watch is dropped too.
func (w *Watcher) Remove(source string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.removeLocked(source)
}

func (w *Watcher) removeLocked(source string) {
	if _, ok := w.tracked[source]; !ok {
		return
	}
	delete(w.tracked, source)

	dir := filepath.Dir(source)
	w.dirCount[dir]--
	if w.dirCount[dir] <= 0 {
		delete(w.dirCount, dir)
		// Ignore the error: the directory may already be gone, which is fine.
		_ = w.fsw.Remove(dir)
	}
}

// isTracked reports whether path is a tracked source file.
func (w *Watcher) isTracked(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.tracked[path]
	return ok
}

// Count returns the number of files currently being watched.
func (w *Watcher) Count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.tracked)
}

// Run pumps fsnotify events, filters them to tracked files, translates them
// into Events, and delivers them on the Events channel until ctx is cancelled.
// It always closes the underlying watcher and the Events channel before
// returning.
func (w *Watcher) Run(ctx context.Context) error {
	defer close(w.events)
	defer w.fsw.Close()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case ev, ok := <-w.fsw.Events:
			if !ok {
				return nil
			}
			w.handle(ctx, ev)

		case _, ok := <-w.fsw.Errors:
			if !ok {
				return nil
			}
			// fsnotify errors are transient (e.g. queue overflow); the periodic
			// rescan reconciles any events missed here, so we keep running.
		}
	}
}

// handle classifies a raw fsnotify event for a tracked file and emits the
// corresponding Event.
func (w *Watcher) handle(ctx context.Context, ev fsnotify.Event) {
	if !w.isTracked(ev.Name) {
		return
	}

	var op Op
	switch {
	case ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
		op = OpRemoved
	case ev.Op&(fsnotify.Create|fsnotify.Write) != 0:
		op = OpModified
	default:
		// Chmod and other ops do not change content; ignore them.
		return
	}

	select {
	case <-ctx.Done():
	case w.events <- Event{Source: ev.Name, Op: op}:
	}
}

# Architecture

This document describes how env-sync is structured and why.

## Overview

env-sync is a single long-running process composed of small, single-purpose
packages. The `daemon` package is the orchestrator; everything else is a leaf
dependency it drives.

```
                         ┌─────────────────────────────────────────┐
                         │                daemon                    │
                         │  (orchestration: when to mirror & sync)  │
                         └───┬───────┬───────┬───────┬───────┬──────┘
                             │       │       │       │       │
                  ┌──────────▼─┐ ┌───▼────┐ ┌▼──────┐ ┌▼─────┐ ┌▼──────┐
                  │  scanner   │ │watcher │ │manifest│ │ git  │ │ logx  │
                  │ discover + │ │fsnotify│ │ JSON   │ │shell │ │logger │
                  │  hash      │ │ events │ │ state  │ │ out  │ │       │
                  └────────────┘ └────────┘ └────────┘ └──────┘ └───────┘
```

The `cli` package builds the effective `config` and hands a `daemon` to one of
the subcommands.

## Packages

| Package             | Responsibility                                                                 |
| ------------------- | ------------------------------------------------------------------------------ |
| `internal/config`   | Load YAML + apply defaults; validate. Pure data, no side effects.              |
| `internal/scanner`  | Walk the scan root, match `.env*` patterns, skip ignored dirs, stream SHA-256. |
| `internal/manifest` | Concurrency-safe JSON store of tracked file state; atomic save.                |
| `internal/watcher`  | Wrap fsnotify; watch **directories**; emit `OpModified` / `OpRemoved` events.  |
| `internal/git`      | Thin wrapper over the system `git` binary (init, status, add/commit/push).     |
| `internal/daemon`   | The engine: reconcile, mirror, react to events, scheduled sync, shutdown.      |
| `internal/cli`      | cobra commands, flag/config merging, logger construction.                      |
| `pkg/logx`          | Minimal leveled logger with secret-safe output.                                |
| `cmd/env-sync`      | `main()` — error → exit code only.                                             |

## Key decisions

### Shell out to `git` instead of a Go git library

The backup repository is a normal git repo the user may also interact with.
Shelling out guarantees identical behaviour to their own CLI — SSH keys,
credential helpers, commit signing, and remotes all just work — and avoids a
large dependency. The only git output env-sync handles is porcelain status and
paths, which contain no secrets.

### Watch directories, not files

Editors commonly save a file by writing `file.tmp` and renaming it over
`file`, which deletes the inotify watch on the original inode. By watching the
*parent directory* (reference-counted across the files we track in it), env-sync
keeps observing the path across atomic saves. Events for untracked files in the
same directory are filtered out.

### Hash-based change detection

Filesystem events are noisy (a single save can emit several writes). env-sync
treats the SHA-256 of file contents as the unit of change: a file is only marked
dirty when its hash actually differs from what the manifest recorded. This is
what keeps commits meaningful instead of one-per-keystroke.

### Git is the source of truth for "needs sync"

The manifest's dirty flags are an optimization, not the authority. Every
scheduled sync runs `git status --porcelain` and commits iff the working tree
changed. Consequently, a dropped inotify event, a crash mid-cycle, or a restart
can never cause a change to be silently skipped — the next sync reconciles it.

### Deletions retain backups

The tool exists because local env files get lost. So when a source file
disappears, env-sync stops *tracking* it but does **not** delete the mirrored
copy. A fat-fingered `rm` therefore can't destroy your backup; the last good
version remains in git history regardless.

### Atomic writes everywhere

Both the manifest and each mirrored file are written to a temporary path and
`rename(2)`-d into place, so a crash mid-write can never leave a corrupt
manifest or a half-written secret.

## Lifecycle of `env-sync start`

1. `git init` / checkout branch on the backup repo (idempotent).
2. Start the fsnotify pump in a goroutine.
3. **Initial reconcile**: scan → mirror changed/new files → update manifest →
   add watches → untrack vanished files.
4. Enter the event loop, selecting over:
   - **watcher events** → mirror or untrack a single file immediately.
   - **rescan ticker** (`rescan_interval`) → full reconcile to pick up new
     projects.
   - **sync ticker** (`sync_interval`) → commit & push if the tree changed.
   - **context cancellation** (SIGINT/SIGTERM) → best-effort final sync, then
     exit cleanly.

## Concurrency

- The manifest guards its map with a `sync.RWMutex`; all exported methods are
  safe for concurrent use.
- The watcher guards its tracked-set and directory ref-counts with a mutex.
- The daemon itself is single-goroutine in its event loop (plus the watcher
  goroutine), so no additional locking is needed at the orchestration layer.

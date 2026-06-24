# env-sync

> Automatic `.env` file backup daemon — never lose your local environment variables again.

`env-sync` runs continuously on your machine, watches every `.env*` file under
your projects directory, and backs them up to a Git repository with batched
commits. Reinstall your machine, clone the backup repo, and restore every
secret you had.

It is written in Go, depends on almost nothing, and ships with a `systemd --user`
unit so it starts automatically on login.

---

## Features

- 🔍 **Recursive discovery** of `.env`, `.env.local`, `.env.production`,
  `.env.development`, `.env.test`, and any `.env.*` file.
- 🪞 **Structure-preserving mirror** — files keep their relative path at any
  depth (`proj/backend/.env` → `proj/backend/.env` in the backup).
- 🔐 **Content-hash change detection** (SHA-256) — only real changes are
  committed, not every filesystem event.
- 👀 **Filesystem watching** via inotify (`fsnotify`) for instant change
  detection, plus periodic rescans to discover brand-new projects.
- ⏱️ **Batched Git sync** every 6 hours (configurable): `add` → `commit` →
  `push`, with timestamped commit messages. Nothing changed? Nothing happens.
- 🗂️ **JSON manifest** tracking source path, backup path, hash and last-sync time.
- 🛡️ **Secret-safe logging** — only filenames and metadata are ever logged,
  never the contents of your env files.
- 🔁 **Reliable** — survives restarts, recreates watchers, retains backups of
  deleted files, and handles editor rename-on-save.

---

## How it works

```
/home/you/projects/my-project/.env             ->  my-project/.env
/home/you/projects/api/.env.production          ->  api/.env.production
/home/you/projects/shop/services/api/.env.local ->  shop/services/api/.env.local
```

The path *relative to your scan root* becomes the path inside the backup repo,
so nesting is preserved no matter how deep the file lives.

```
              scan + watch                       every 6h
 projects/ ───────────────► mirror into repo ───────────────► git commit & push
   .env*                     (+ SHA-256 hash)                  (only if changed)
```

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design.

---

## Installation

### From source

Requires Go 1.22+ and `git`.

```bash
git clone https://github.com/bouchiba/env-sync.git
cd env-sync
make build            # produces ./bin/env-sync
```

### Install for your user (binary + config + systemd unit)

```bash
make install          # installs to ~/.local/bin, ~/.config/env-sync
```

Make sure `~/.local/bin` is on your `PATH`.

---

## Quick start

1. **Create a backup repository** (a private remote is strongly recommended):

   ```bash
   mkdir -p ~/.env-sync/backup && cd ~/.env-sync/backup
   git init -b main
   git remote add origin git@github.com:you/env-backups.git   # optional but recommended
   ```

   > If you skip the remote, env-sync still keeps a **local** commit history as
   > a backup — but it won't survive a disk wipe. Add a private remote.

2. **Configure** (optional — defaults work out of the box):

   ```bash
   cp config/config.example.yaml ~/.config/env-sync/config.yaml
   $EDITOR ~/.config/env-sync/config.yaml
   ```

3. **Run it**:

   ```bash
   env-sync start --config ~/.config/env-sync/config.yaml
   ```

   Or run a one-off backup right now:

   ```bash
   env-sync sync
   ```

---

## CLI

```
env-sync start     Run the daemon in the foreground (watch + scheduled sync)
env-sync scan      Discover .env files and refresh the manifest (no commit)
env-sync sync      Scan, then commit & push pending changes immediately
env-sync status    Show tracked files, hashes and last-sync times
```

Global flags (override the config file):

```
-c, --config           path to YAML config file
    --log-level         debug|info|warn|error      (default info)
    --scan-root         directory tree to scan
    --repo-path         local git backup repo path
    --git-branch        branch to push to
    --git-remote        remote to push to
    --manifest-path     path to the JSON manifest
    --sync-interval     how often to commit & push (e.g. 6h)
    --rescan-interval   how often to rescan for new files (e.g. 1h)
```

Example `status` output (note: **no secret values**, only metadata):

```
Scan root:  /home/you/projects
Repo path:  /home/you/.env-sync/backup
Tracked:    3 file(s)

BACKUP                        HASH          DIRTY  LAST SYNC
api/.env.production           eb2da4357e30  no     2026-06-24T03:58:18Z
my-project/.env               641d00dcab6e  no     2026-06-24T03:58:18Z
shop/services/api/.env.local  9f1c0b7a2d44  yes    never
```

---

## Configuration

Configuration is read from a YAML file (path via `--config`); any field can be
overridden by the matching CLI flag. All fields are optional and fall back to
the defaults below.

| Field             | Default                                 | Description                                   |
| ----------------- | --------------------------------------- | --------------------------------------------- |
| `scan_root`       | `~/projects`                            | Directory tree scanned for `.env*` files      |
| `repo_path`       | `~/.env-sync/backup`                    | Local Git repo the files are mirrored into    |
| `sync_interval`   | `6h`                                    | How often changes are committed & pushed      |
| `rescan_interval` | `1h`                                    | How often to rescan for new projects          |
| `git_branch`      | `main`                                  | Branch pushed to                              |
| `git_remote`      | `origin`                                | Remote pushed to                              |
| `manifest_path`   | `<repo_path>/.env-sync-manifest.json`   | JSON manifest location                        |
| `ignore_dirs`     | `.git node_modules vendor dist build .next` | Directory names skipped while scanning    |
| `file_patterns`   | `.env`, `.env.*`                        | Glob patterns matched against file names      |

Durations use Go's format: `30m`, `1h`, `6h`.

See [config/config.example.yaml](config/config.example.yaml) for a complete
annotated example.

---

## Running as a service (systemd `--user`)

`make install` already copies the unit. To enable it:

```bash
systemctl --user daemon-reload
systemctl --user enable --now env-sync.service

# Keep it running even when you're logged out:
loginctl enable-linger "$USER"
```

Check status and logs:

```bash
systemctl --user status env-sync
journalctl --user -u env-sync -f
```

The unit lives at [systemd/env-sync.service](systemd/env-sync.service) and is
hardened (`NoNewPrivileges`, `ProtectSystem=strict`, restart-on-failure).

---

## Security

- **Secrets are never logged.** Logs and `status` output contain only file
  paths, SHA-256 hashes and timestamps — never the contents of an env file.
- Mirrored files are written with `0600` permissions.
- Your secrets *do* live in the backup repository. **Use a private remote** and,
  ideally, an encrypted-at-rest host. env-sync deliberately does not invent its
  own encryption scheme; pair it with your own (e.g. `git-crypt` or
  `transcrypt`) if you need encrypted blobs.

---

## Reliability notes

- **Restarts**: state is persisted in the manifest; on startup env-sync rescans
  and reconciles, so nothing is lost.
- **Deleted files**: when a source `.env` disappears, env-sync stops tracking it
  but **keeps the last backed-up copy** — an accidental delete never wipes your
  backup.
- **Editor saves**: many editors save by writing a temp file and renaming it
  over the original. env-sync watches parent directories and treats this as a
  modification, so atomic saves are detected correctly.
- **Source of truth for sync**: every scheduled sync asks `git status` whether
  anything actually changed, so a missed inotify event can never silently skip
  a backup — the next sync still catches it.

---

## Development

```bash
make test      # run the test suite
make vet       # go vet
make fmt       # gofmt -s
make build     # build ./bin/env-sync
```

Project layout:

```
cmd/env-sync/      binary entry point
internal/config/   configuration loading & validation
internal/scanner/  recursive .env discovery + hashing
internal/manifest/ JSON state store
internal/watcher/  fsnotify wrapper
internal/git/      git command wrapper
internal/daemon/   orchestration (the engine)
internal/cli/      cobra commands
pkg/logx/          tiny leveled logger
```

---

## License

MIT — see [LICENSE](LICENSE).

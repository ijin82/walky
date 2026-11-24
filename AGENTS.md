# Instructions for AI assistants (AGENTS.md)

Read this file in full before the first edit of a session. If a tool's default
habits conflict with it, this file wins.

## 1. What this project is

`Walky` is an offline "cloud" on a USB drive: two-way synchronization of a local
folder with a store on the drive, including version history, deduplication,
conflict handling and restore onto a clean machine. There is no internet; the
transport is a physical flash drive.

- Core and CLI are Go (`core/`); the daemon runs in a systemd user session.
- UI is Vala + GTK4/libadwaita (`ui/`), a separate process that talks over D-Bus
  only.
- The drive is ext4 (optionally inside LUKS2) with a `walky.json` marker; the
  on-drive format is versioned. Other filesystems are rejected (see
  `store.CheckFilesystem`). LUKS is optional and never enforced, but strongly
  recommended: a lost or stolen drive without encryption exposes all data.
- Architecture and format rules — `docs/DESIGN.md` (the source of truth for the
  on-drive format and commit protocol). Live status and checklists — `PLAN.md`.

The target workload is about 15 GB with many small files and git repositories.
Decisions that make such trees slower (extra `fsync`, rehashing the whole tree on
every step, one standalone object per small file) are not acceptable without
discussion.

## 2. Language policy

**Everything inside the repository is written in English.** This is not limited
to code:

- source code, identifiers, comments and doc comments;
- documentation: `README.md`, `PLAN.md`, `docs/DESIGN.md`, `AGENTS.md`;
- commit messages, branch names, tag annotations;
- release notes and GitHub Release texts;
- configuration examples, systemd units, udev rules, sysctl files, desktop
  entries;
- test names, log messages, CLI help, error strings and user-visible UI strings;
- comments inside configuration and data files.

Russian is used **only in live conversation with the user**. Never add Russian
text to a file in this repository, even as a temporary note or a TODO. When
editing a file that still contains another language, translate it as part of the
change. Third-party names, paths and commands keep their original spelling.

## 3. Structure and boundaries

```
core/                  Go module "walky" (built independently)
  cmd/walky/           CLI and daemon
  internal/store/      CAS, manifests, commit, restore, verify, packs
  internal/scan/       tree walk, hashing, excludes, index use
  internal/index/      SQLite hash cache
  internal/engine/     one snapshot run shared by CLI and daemon
  internal/daemon/     state machine, device lifecycle, recent events
  internal/device/     mount table watcher, marker checks, eject, sleep
  internal/api/        D-Bus service (methods, signals)
  internal/state/      machine-local paths
  internal/config/     configuration and validation
ui/                    Vala + GTK4/libadwaita, its own Makefile (valac)
data/                  systemd, udev, sysctl, .desktop, example config
docs/DESIGN.md         architecture, pitfalls, format
```

Rules:

- `core/` knows nothing about GTK; `ui/` knows nothing about the on-drive
  format. The contract is `core/internal/api/walky1.xml`.
- New subsystems live in their own `internal/...` packages, not in `cmd`.
- Go and Vala are never mixed in one directory; the builds stay independent.
- The on-drive format changes only through `FormatVersion` plus a migration; an
  older binary must not write a newer format.

## 4. Build and checks

Before finishing any iteration:

```sh
make check        # gofmt + go vet + go test + build both binaries
```

- `make build` produces `bin/walky` and `bin/walky-ui`.
- `make run-ui` starts the window when a graphical session is available.
- Tests are part of a change. New behavior without a test is not done.
- The storage commit protocol requires failpoint tests: a failure at any step
  must not move `HEAD` or break `verify`.
- Never disable or weaken existing tests to get a green run.

### 4.1 Testing policy

Everything that can be tested without special hardware **must** be covered by
automated tests. "Works on my machine" is not acceptance.

- Pure logic (CAS, manifests, commit, restore, verify, scan, index, excludes,
  config) — unit tests.
- Protocol and durability — failpoint and property tests; `SIGKILL` at every
  commit step must leave a consistent store.
- CLI commands — integration tests against temporary directories.
- D-Bus and device code — tests against fake objects / a loop-backed store where
  the platform allows it.
- New behavior ships with tests in the same change. A bug fix starts with a
  failing regression test.
- Do not mock the filesystem in storage tests: real temp directories (and loop
  devices later) are required, because crash and rename semantics are the point.
- `go test -cover ./...` (or `make cover`) is for visibility, not a score. A
  large uncovered area must be explained, not hidden.
- Never weaken a test to make a build pass.

Manual testing is the exception, not the default. Only these may stay manual:
a physical drive being plugged and unplugged, LUKS passphrase prompts, GNOME
Shell visuals (tray, portal, notifications), real suspend/resume, real USB
throughput, and actual power loss / fsck behaviour. They are listed with steps
and expected results in `docs/MANUAL_TESTS.md`; update that file when behavior
changes. When a manual check finds a bug, add an automated regression test if it
is technically possible.

## 5. Data and durability

Invariants that must not be broken:

1. `HEAD` always points at a fully written manifest whose objects all exist.
2. The local `base` moves only after a successful commit on the drive.
3. No file is deleted without a copy in the trash.
4. An interruption at any moment leaves the next run able to roll back garbage
   or finish the commit; there is no half-written `HEAD`.
5. Reads always go through `HEAD`; extra objects are ignored and collected by GC.

Additional rules:

- Hash is blake3; verify on write and on restore.
- Restore goes through `os.Root` / `filepath.IsLocal` only: no `..`, no absolute
  paths, no symlink traversal.
- Do not `fsync` every file: batch writes and `syncfs` before `HEAD`.
- Deduplicate only against objects referenced by a committed `HEAD`
  (`CommittedHashes`), never against mere file presence: leftovers from a crash
  may be truncated.
- A pack must be fsynced before its index is published.
- No network calls: the project is offline by design.
- `walky init` never formats a drive; `mkfs`/LUKS is a separate command with
  confirmation. Accidentally wiping a disk is unacceptable.
- Every user-facing path checks `store.CheckFilesystem` before creating or
  opening a store; do not add an "any filesystem" escape hatch.

## 6. Documentation

- Keep `README.md`, `PLAN.md` and `docs/DESIGN.md` in sync with behavior:
  commands, limitations, milestone status. `PLAN.md` holds the live status and
  checklists; `DESIGN.md` holds architecture and accepted decisions.
- Do not invent platform facts (systemd, udisks2, GTK); when unsure, check the
  system or the documentation.
- In replies, link files with relative paths and line numbers when that helps.

## 7. Git

- **Default rule: commit only on an explicit request from the user.** By default
  work stays in the working tree.
- **Temporary exception (in force until the user revokes it):** once a change is
  complete and `make check` is green, commit it without asking. One logical step
  per commit, English message, no push and no tags. If checks fail, do not
  commit. This permission is scoped to this repository and must not be assumed
  elsewhere.
- One logical step per commit; messages in English, imperative mood
  (`store: reject unsafe manifest paths`).
- Do not commit unfinished work, push, or create tags without a request.
- Do not reformat unrelated files or "fix" unrelated code in the same commit.
- Never touch or revert the user's uncommitted changes.

## 8. Iterations and local deployment

When the current iteration is complete (the task is done and `make check` is
green):

```sh
make install-local
```

- Installs fresh `walky` and `walky-ui` into `~/.local/bin` and the desktop
  entry into `~/.local/share/applications`, without root, so the user runs the
  new build.
- Do **not** commit at this point; deploy and report only.
- If the iteration is incomplete or checks are red, do not deploy.
- System-wide installation (`sudo make install install-data`) and enabling
  `walky.service` happen only on request and only once the daemon actually works
  (M3).

## 9. Releases

- The version lives in `VERSION` and is injected at build time via `-ldflags`.
- Semver: `patch` for fixes, `minor` for new features, `major` for on-drive
  format changes or incompatible CLI changes.
- Release notes are English, stored in `dist/GITHUB_RELEASE_vX.Y.Z.md` (`dist/`
  is gitignored), with sections: Highlights, Added, Changed, Fixed, Upgrade
  notes, Tests, Artifacts/SHA-256.
- Tags and pushes only on explicit request; before tagging, `make check` must be
  green and the tree clean.
- Never promise unimplemented features in release notes; check milestone status
  against `PLAN.md`.

## 10. UI and GNOME

- Stack is GTK4 + libadwaita; no custom skins.
- **No unnecessary dialogs.** A dialog is allowed only for settings, conflict
  resolution and confirmation of a destructive action (format, overwrite).
  Everything else uses banners, toasts, notifications or status rows.
- Long operations run off the main loop; progress is reported through D-Bus
  signals with throttling.
- The app must be visible as running: the Background portal and/or a
  StatusNotifierItem, plus `Gio.Notification` on completion and errors.
- The UI never blocks synchronization: a crashed window must not take the daemon
  down.

## 11. Do not

- Add dependencies without a reason; justify every new one.
- Change the on-drive format silently.
- Log file contents or secrets; paths and hashes are fine.
- Use `sudo` or install packages without a request.
- Delete or rewrite user data on the drive outside the protocol
  (tmp → fsync → rename).

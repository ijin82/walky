# Walky development plan

Living document: milestone status, next tasks and acceptance criteria.
Architecture, storage format and the reasoning behind decisions live in
[docs/DESIGN.md](docs/DESIGN.md); this file tracks the plan and progress.

Version: `0.1.0` · updated: 2026-11-24

## Milestone status

| Milestone | Scope | Status | Estimate |
|---|---|---|---|
| M0 | Skeleton: repository, CLI, config, systemd/udev, empty Vala window | ✅ done | 0.5–1 d |
| M1 | Storage: CAS, generations, commit, restore, verify | ✅ done | 3–4 d |
| M2 | Scan index + packs for small files | ✅ done | 3–4 d |
| M3 | Daemon: drive detection, D-Bus, systemd unit, events | ✅ done | 2–3 d |
| M4 | Two-way sync: base, deletions, conflicts | ✅ done | 5–7 d |
| M5 | Retention and GC | ⏳ next | 2–3 d |
| M6 | UI: status, progress, last 5 files, eject | ⬜ planned | 4–6 d |
| M7 | Packaging, rpm/ALT, man page, docs, sysctl | ⬜ planned | 2–3 d |
| M8 | LUKS/format, manifest signing, fuzz, fault injection | ⬜ planned | 3–5 d |

Estimates assume one developer working part-time; full-time is roughly twice as
fast.

## Done

### M0 — skeleton

- Split into `core/` (Go) and `ui/` (Vala) with independent builds.
- `walky` CLI: `version`, `status`, `daemon`, command dispatch.
- TOML config with validation and defaults, `machine-id`.
- `data/`: systemd user unit, udev rule, inotify sysctl, example config,
  `.desktop` entry.
- `make check`, `make install-local`, version in `VERSION`.

### M1 — storage

- `walky.json` marker (`format`, `repo_id`), `Init/Open`, refusal to adopt a
  non-empty directory without `--force`; `init` creates the store directory.
- blake3 CAS (`objects/ab/cd/<hash>`), tmp → rename writes.
- Generations: JSONL + zstd manifest, meta file, `HEAD` written last, one
  `syncfs` per commit.
- `Commit` is idempotent and checks that every object exists before `HEAD` moves.
- `Restore` through `os.Root`: modes, mtime, symlinks, empty directories, hash
  verification while copying.
- `Verify` quick/full, failpoint tests for the commit protocol.
- CLI: `init`, `snapshot`, `ls`, `head`, `restore`, `verify`.

### M2 — index and packs

- SQLite hash cache keyed by `(dev, ino, size, mtime, ctime)`; pruned on scan,
  reset with `--force-rescan`.
- Packs: objects under 1 MiB, 32 MiB target, one zstd frame per object, binary
  `.idx` with `hash + offset + compLen + rawLen`.
- Pack catalog loaded lazily, binary search; object reads transparently handle
  standalone files and packs.
- Durability: pack fsynced before its index; upload dedup relies on committed
  `HEAD` hashes only; `PutObject` replaces existing files atomically.
- `BenchmarkWalk200k` (200k files across 2000 directories).

### M3 — daemon

- `internal/engine`: one snapshot run shared by CLI and daemon, cancellable
  between files, progress callbacks per phase and per uploaded file.
- `internal/device`: mount table watcher (2 s poll by default), marker
  validation, `WALKY/` and mount-root layouts, attach/detach diffing, udisks2
  eject/power-off, logind `PrepareForSleep` watcher.
- `internal/daemon`: state machine (`no_device`, `idle`, `syncing`, `paused`,
  `error`), auto-sync on attach, pause/resume, cancellation on detach, ring
  buffer of the last 200 events, free-space reporting, integrity check.
- `internal/api`: `io.github.ijin.Walky1` on the session bus with `SyncNow`,
  `Pause`, `GetStatus`, `GetEvents`, `Eject`, `Verify` and the
  `StateChanged`/`Progress`/`FileDone` signals.
- `walky daemon` claims the bus name (second instance exits cleanly), polls the
  mount table, validates `repo_id`, refuses drives that overlap the sync folder.
- `walky status` queries the daemon and falls back to local output; `walky sync
  [--wait]` and `walky eject` go through D-Bus.
- systemd user unit `walky.service` (`Type=dbus`, `BusName`) is ready for
  installation; `--device` and `--repo` flags allow manual and test runs.

**Not done in M3:** mounting a drive that the desktop did not automount. The
daemon consumes already-mounted filesystems; mounting through udisks2 without a
polkit prompt is a follow-up. Detection itself is mount-table based rather than
udisks2-signal based — equivalent in practice and testable without hardware.

## Measured (NVMe, 12 threads)

| Scenario | Result |
|---|---|
| Scan 200k files, cold (all hashes) | 2.3 s |
| Scan 200k files, warm (index) | 1.2 s |
| First snapshot of 20k files / 1.1 MiB | 1.2 s |
| Repeated snapshot with no changes | 0.2 s |
| Increment of 100 files out of 20k | 0.6 s |
| RSS for 20k files | 52–60 MB |
| Integrity check after packing | ok |
| Daemon attach → first generation (2 files, private bus) | < 1 s |

Uploading 15 GB to a real flash drive has not been measured yet: it needs the
hardware and both USB 2.0 and USB 3.0.

## Done: M4 — two-way sync

- Local base manifest (`~/.local/state/walky/base-<repo>.jsonl.zst` with a meta
  file) and per-machine state on the drive (`machines/<id>.json`).
- 3-way plan per path (local / drive / base): push, pull, delete local, delete on
  the drive, keep both on conflict. Deletions are inferred from the base, so the
  drive manifest stays a plain full tree.
- Conflicts: `copy` (local renamed to `name.conflict-<host>-<time>.ext`, remote
  content restored at the original path, the copy is pushed like any other
  file), `local-wins`, `newest`.
- Deletions move files to the local trash and record the removed entries in
  `trash/<gen>.json` on the drive for retention.
- Pulls are applied first, atomically (temp file in the target directory, hash
  verification, rename, mode/mtime), so a failed push never leaves a corrupt
  tree; the base advances only after a successful commit.
- Free-space preflight for both the drive and the repository; a conditional
  commit (`ExpectParent`) refuses to overwrite a generation written by another
  machine during the sync.
- Automated tests: merge-plan table, two-machine convergence, deletions and
  trash, conflict copy / local-wins, stale-parent commit, randomized
  200-operation convergence with store verification.

Deviations recorded: tombstone entries are not stored in manifests yet —
deletions are derived from the base, which is correct for the supported
scenarios; a machine whose base is older than trash retention is not yet
special-cased. Both belong to M5.

## Next iteration — M5

Goal: history stays bounded and the drive does not fill up with dead objects.

- [ ] Retention policy 14 daily / 8 weekly / 6 monthly, 30-day trash.
- [ ] Mark-and-sweep over retained manifests, `machines/*.json` bases and
      `trash/*.json`; packs and standalone objects included.
- [ ] `walky gc`, reclaimed-space report, safe rebuild of pack caches.
- [ ] Guard: never run GC while dirty, during sync, or without free space.
- [ ] Stale-base handling: warn or force a merge when a machine's base is older
      than the oldest retained manifest.
- [ ] Tests: GC after 100 generations leaves no dangling references; `verify`
      stays clean; retention keeps the requested generations.

**M5 DoD:** `verify --full` passes after GC; objects referenced by every retained
generation survive; space is actually reclaimed; automated tests cover the
mark-sweep.

## Later milestones

### M6 — UI

- [ ] libadwaita window: status, progress, throughput, ETA.
- [ ] "Last 5 files" plus a full event list from `GetEvents`.
- [ ] Device card: label, UUID, free space, "Eject".
- [ ] Settings: excludes, retention, conflict policy.
- [ ] Notifications, Background portal and/or SNI tray icon.
- [ ] UI starts the daemon over D-Bus and never blocks sync.

### M7 — packaging

- [ ] `rpm` spec for ALT, `make install` + `install-data`.
- [ ] man page, README, recovery guide.
- [ ] inotify sysctl and udev rule shipped in the package.

### M8 — hardening

- [ ] `walky format`: mkfs.ext4 + LUKS2, confirmation, refuse mounted devices.
- [ ] Keyfile/passphrase, optionally TPM.
- [ ] Manifest signing (ed25519), `repo_id` allowlist.
- [ ] `dm-flakey`/`dm-error`, fuzz manifest and `.idx` parsers, 24-hour soak.

## Iteration definition of done

1. Behavior is covered by tests; existing tests are not weakened.
2. `make check` is green.
3. README and this file are updated; on format changes, `docs/DESIGN.md` too.
4. `make install-local` has been run (the user has the fresh binaries).
5. Manual-only checks affected by the change are updated in
   [docs/MANUAL_TESTS.md](docs/MANUAL_TESTS.md).
6. Commit only on explicit request; the working tree keeps the changes.

## Acceptance metrics

| Metric | Target | Actual |
|---|---|---|
| First upload of 15 GB, USB 3.0 | 2–5 min | not measured |
| First upload of 15 GB, USB 2.0 | 10–17 min | not measured |
| Increment of ~100 files | < 5 s | 0.6 s (20k files) |
| Warm scan of 200k files | < 5 s | 1.2 s |
| Daemon RSS | < 150 MB | 52–60 MB (CLI) |
| Idle CPU | < 1% | not measured |
| Data loss under failures | 0 (invariants I1–I5) | tests + failpoints |

## Open questions

1. History depth: 14/8/6 or "last 30 versions"? It drives the drive size.
2. Auto-sync on plug (`auto_sync = true` today) or a button?
3. Do we need a transparent `current/` tree on the drive (hardlink farm, ext4
   only)?
4. One drive or two for rotation (two is recommended)?
5. LUKS: keyfile + passphrase, passphrase only, or TPM?

## Accepted decisions

- The drive is read by Linux only → ext4 + LUKS2.
- Multiple machines → `machine-id`, per-machine base, generations.
- Conflicts → keep both variants.
- Stack → Go core/CLI + Vala/GTK4 UI over D-Bus.

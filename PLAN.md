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
| M5 | Retention and GC | ✅ done | 2–3 d |
| M6 | UI: status, progress, last 5 files, eject | 🟡 MVP done | 4–6 d |
| M7 | Packaging, rpm/ALT, man page, docs, sysctl | ⏸ deferred | 2–3 d |
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
  2000-operation convergence with store verification.

Deviations recorded: tombstone entries are not stored in manifests; deletions
are derived from the base, which is correct for the supported scenarios. GC now
retains every machine's base generation, so a returning machine always has a
manifest to merge against; explicit tombstones remain optional future work.

## Done: M5 — retention and GC

- Retention policy: 14 daily / 8 weekly / 6 monthly by default, configurable in
  `[retention]`; HEAD is always kept, and the base generation of every known
  machine is retained until that machine record is removed.
- Mark phase reads every kept manifest plus non-expired `trash/*.json` entries;
  everything else is unreachable.
- Sweep removes unreferenced standalone objects, deletes fully dead packs and
  compacts mixed packs into fresh ones (duplicates that also exist standalone or
  in retained packs are not copied). Compaction checks free space first and
  falls back to deleting only dead packs.
- Trash files older than `trash_days` expire with their objects; snapshot files
  for generations outside the policy are removed last.
- `walky gc --store … [--dry-run] [--daily N] [--weekly N] [--monthly N]
  [--trash-days N]` reports kept/removed generations, objects, packs, expired
  trash and reclaimed bytes.
- GC takes the store write lock, refuses to run without HEAD, and never runs
  while another commit is in progress; it is intended to be started when the
  daemon is idle.
- Tests: retention, machine bases, pack compaction, duplicate handling, dry run,
  trash expiry, missing HEAD, and 100 generations with `verify --full` after GC.

## Done: M6 — UI MVP

- Vala/GTK4 window with libadwaita: state, device and store path, generation,
  last sync, free space.
- Live progress (done/total) and a "Recent files" list fed by `GetEvents`
  every second.
- Actions: sync now, pause/resume, verify, eject, quit; menu plus buttons.
- Banner for errors and `Gio.Notification` when a sync finishes or fails.
- `walky-ui` connects to the daemon over D-Bus; if the daemon is not running it
  shows that state instead of hanging. The daemon remains the only writer.
- Manual trash management: `walky trash list` and `walky trash empty`
  (`--store` clears the drive trash and reclaims space, `--local` clears the
  machine's trash, `--dry-run` reports).

Remaining for a full M6: settings UI (excludes, retention, conflict policy),
Background portal / StatusNotifierItem so the app is visible after closing the
window, and starting the daemon from the UI when it is not running.

## Next iteration — M6 follow-ups

- [ ] Settings window: excludes, retention, conflict policy (write the config).
- [ ] Background portal or SNI tray icon; keep the app visible when the window
      is closed.
- [ ] Start the daemon over D-Bus (systemd activation) when unavailable.
- [ ] Manual checks MT-20…MT-22 in `docs/MANUAL_TESTS.md`.
Packaging is deferred at the user's request; the installation path for now is
`./install.sh` plus this README.

### M8 — hardening (optional, not required)

Nothing here is needed to use an existing ext4 drive. `walky format` would be a
convenience for blank drives (it would create ext4, optionally inside LUKS);
encryption is the only feature that requires creating a new container.

- [ ] Optional `walky format`: mkfs.ext4 (+LUKS2), confirmation, refuse mounted
      devices. Never invoked implicitly.
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

- The drive is read by Linux only → ext4 + LUKS2, and every other filesystem is
  rejected at init/open (LUKS-wrapped ext4 is fine once mounted).
- Windows/macOS support is explicitly out of scope: those systems cannot mount
  ext4, and giving up ext4 for plug-and-play readability was considered and
  rejected. Reading elsewhere means a Linux live environment or WSL2 with the
  normal `walky` CLI (`walky restore`).
- LUKS2 is strongly recommended for portable drives. Tooling is M8, so
  `require_luks` is advisory and not enforced yet.
- Multiple machines → `machine-id`, per-machine base, generations.
- Conflicts → keep both variants.
- Stack → Go core/CLI + Vala/GTK4 UI over D-Bus.

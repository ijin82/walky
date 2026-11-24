# Walky development plan

Living document: milestone status, remaining work and acceptance criteria.
Architecture and format rules live in [docs/DESIGN.md](docs/DESIGN.md); this file
tracks the plan.

Version: `0.1.0` · updated: 2026-11-24

## Milestone status

| Milestone | Scope | Status |
|---|---|---|
| M0 | Skeleton: repository, CLI, config, systemd/udev, Vala window stub | ✅ done |
| M1 | Storage: CAS, generations, atomic commit, restore, verify | ✅ done |
| M2 | Scan index and packs for small files | ✅ done |
| M3 | Daemon: drive detection, D-Bus, systemd unit, events | ✅ done |
| M4 | Two-way sync: bases, deletions, conflicts, trash | ✅ done |
| M5 | Retention and GC | ✅ done |
| M6 | Desktop UI | ✅ done |
| M7 | Packaging (rpm/ALT, man page) | ⏸ deferred |
| M8 | Optional hardening (format helper, encryption tooling) | ⬜ optional |

## Done

- **Storage** — blake3 CAS, packs, JSONL/zstd manifests, generations, `HEAD`
  written last, `syncfs` instead of per-file fsync, restore through `os.Root`,
  `verify` quick/full, failpoint tests.
- **Scan** — SQLite hash cache keyed by stat signature, excludes, `--force-rescan`,
  200k-file benchmark (2.3 s cold / 1.2 s warm).
- **Daemon** — mount-table watcher, marker and `repo_id` validation, auto-sync,
  pause, cancellation, D-Bus API and signals, logind sleep handling, udisks2
  eject and automount of unmounted filesystems labelled `WALKY`.
- **Two-way sync** — per-machine bases, 3-way plan, deletions with local trash
  and drive trash records, conflict copies / `local-wins` / `newest`, conditional
  commits, randomized two-machine convergence tests.
- **Maintenance** — retention policy, mark-and-sweep, dead pack removal, mixed
  pack compaction, trash expiry, `walky gc`, `walky trash list|empty`.
- **UI** — GTK4/libadwaita window over D-Bus: status, device, generation, free
  space, progress, last five files, sync/pause/verify/eject, notifications.
- **UI settings** — excludes, retention and conflict policy are edited in a
  preferences window and sent over D-Bus; the daemon validates, persists and
  applies them immediately. Closing the window keeps the app alive.
- **Tray** — the daemon owns a StatusNotifierItem (AppIndicator) with a menu for
  sync, pause, open, verify and quit; the icon path is independent of the UI.
  A missing daemon is started through systemd on demand.
- **Setup** — `walky init` and the GUI wizard share `internal/setup`; the wizard
  lists mounted removable ext4 drives, initializes `/WALKY`, writes `repo.path`
  and `repo_id`, and can adopt an existing store.
- **Recovery** — the local base records its `repo_id`. A drive from a different
  store enters `needs_recovery` instead of syncing silently; the GUI shows an
  additive preview (no deletions, conflicts keep both), then adopts the new
  `repo_id` and resets the base.
- **Packaging surface** — `install.sh` (local/system/uninstall), systemd user
  unit, `.desktop`, icon in hicolor (SVG + PNG 16–256), `VERSION`.
- **Policy** — Linux-only, ext4-only enforced at init/CLI/daemon; LUKS optional
  and never enforced, but strongly recommended; the standalone extractor was
  removed, recovery is `walky restore`.

## Remaining work

### P1 — finished

Settings, tray, daemon autostart and udisks automount are in place. Only manual
hardware/session checks remain.

### P1 — desktop follow-ups

- [x] Settings window with immediate apply (D-Bus `GetSettings`/`SetSettings`,
      daemon validates and rewrites the config).
- [x] Keep the app alive after the window closes (window is hidden instead of
      destroyed).
- [x] Start `walky.service` through the user systemd manager when the daemon is
      not running.
- [x] StatusNotifierItem tray icon owned by the daemon; no Background portal.
- [x] GUI initialization wizard on top of the shared setup code.
- [ ] Run the manual acceptance checklist below on real hardware/session.

### P1 — drive lifecycle

- [x] Mount a Walky drive through udisks2 when the desktop did not automount it
      (labelled `WALKY`, `noatime`, polkit-aware, retried every 5 s).
- [ ] Do not attempt LUKS unlocking from Walky: the desktop handles the
      passphrase; Walky only waits for the ext4 mount to appear.
- [ ] Real-hardware checks MT-10…MT-14 (plug/unplug, eject, suspend, daemon
      restart).

### P2 — maintenance and long-running behaviour

- [x] Scheduled GC in the daemon: every `gc_interval_hours` while idle and not
      syncing.
- [x] Prune `machines/*.json` records idle for `machine_days`, together with
      their base generations.
- [x] Surface GC results as a `gc` event and a log line with freed bytes.
- [ ] Rotate `log/` on the drive — nothing writes there yet, so this waits for
      on-drive logging.

### P2 — quality

- [x] CLI integration tests for `init`, `snapshot`, `verify`, `restore`, `gc`,
      `trash`, `sync --direct` and config errors. The filesystem check is a
      package seam so tests can run on temporary directories.
- [x] D-Bus service test over a private bus (`dbus-daemon`).
- [x] Fuzz manifest and pack-index parsers (short runs clean).
- [ ] Fault injection with `dm-flakey`/`dm-error` on loop devices (root-only,
      documented in `docs/MANUAL_TESTS.md`).

### P2 — LUKS guidance

- [x] Non-blocking warning from `walky init` when the ext4 mount is not on
      dm-crypt: “LUKS2 is strongly recommended”. Never refuses, never prompts;
      detection walks mountinfo and sysfs slaves.
- [x] Expose the encryption state in `walky status` and the UI (nice-to-have).

### P3 — M8 hardening (optional)

- [ ] Optional `walky format`: `mkfs.ext4` and, with an explicit flag, a LUKS2
      container. Confirmation required, mounted devices refused, never implicit.
- [ ] Manifest signing (ed25519) with a `repo_id` allowlist.
- [ ] Long-running soak and crash tests across machines.

### Deferred

- [ ] Packaging: rpm spec for ALT, man page, package the udev rule and inotify
      sysctl. `install.sh` + README remain the supported path for now.

## Known limitations

- Linux only; ext4 only. Windows/macOS cannot mount the drive.
- Automount only considers filesystems with the configured `[device] label`
  (default `WALKY`); when the desktop mounts the drive, the label is irrelevant
  and detection uses `walky.json`.
- The UI polls status once per second; it does not subscribe to signals yet.
- Deletions are inferred from the base; explicit tombstone entries are not
  stored in manifests.
- In-flight syncs are protected by a conditional commit, but GC is intended to
  run while the daemon is idle.

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

## Definition of done for an iteration

1. Behavior is covered by tests; existing tests are not weakened.
2. `make check` is green.
3. README, PLAN and DESIGN are updated when behavior changes.
4. `make install-local` has been run.
5. Manual-only checks affected by the change are updated in
   [docs/MANUAL_TESTS.md](docs/MANUAL_TESTS.md).
6. Commit per `AGENTS.md` §7 (temporary auto-commit after a green `make check`,
   otherwise only on request).

## Manual acceptance checklist

Automated coverage is `make check`; this list is for what needs a real drive,
LUKS prompt, GNOME session, suspend or power loss. Steps and expected results
are in [docs/MANUAL_TESTS.md](docs/MANUAL_TESTS.md); run the affected items for
every change and all of them before a release.

### Storage and recovery

- [ ] MT-01 CLI init, repeat init rejected, nothing outside `/WALKY`
- [ ] MT-02 first snapshot of 15 GB and `verify --full`, record time/RSS
- [ ] MT-03 increment: only changed files hashed, store grows by the diff
- [ ] MT-04 restore on a clean machine: content, modes, symlinks, mtimes, `git fsck`
- [ ] MT-05 unplug mid-snapshot: old `HEAD` intact, `verify` passes
- [ ] MT-06 corrupt pack/object: `verify` fails, `restore` refuses
- [ ] MT-07 no free space: clear error, `HEAD` intact, drive mountable
- [ ] MT-08 real USB 3.0/2.0 throughput recorded
- [ ] MT-09 retention/GC on a real drive: dry-run, kept buckets, space freed
- [ ] MT-10 trash: list, restore, expiry, `gc --trash-days 0`

### Setup and configuration

- [ ] MT-11 GUI "Set up a drive" initializes and starts the first sync
- [ ] MT-12 GUI "Use this drive" adopts an existing store without rewriting it
- [ ] MT-13 `walky init --repo` writes config and the daemon attaches
- [ ] MT-14 non-LUKS warning is advisory; LUKS drive needs no passphrase from Walky

### Drive lifecycle and daemon

- [ ] MT-15 plug detection and automount with the configured label
- [ ] MT-16 autosync on attach, one notification, recent files update
- [ ] MT-17 safe eject during or after a sync
- [ ] MT-18 suspend during a commit, resume cleanly
- [ ] MT-19 daemon restart mid-sync, no data loss
- [ ] MT-20 daemon stopped: friendly banner with Start, no raw GDBus error
- [ ] MT-21 enable the service, log out/in, daemon and tray start automatically
- [ ] MT-22 tray Quit stops daemon and UI; window Quit keeps the daemon

### Two-way sync

- [ ] MT-23 two-machine conflict keeps both variants with the documented name
- [ ] MT-24 deletion on A does not resurrect from B's stale copy
- [ ] MT-25 offline machine returns after many changes: clean 3-way merge

### Desktop UI and tray

- [ ] MT-26 window shows state, device, generation, progress, last five files
- [ ] MT-27 symbolic tray icon recolors in light/dark themes; menu actions work
- [ ] MT-28 exactly one notification per sync/error event
- [ ] MT-29 settings (excludes, retention, conflict) apply and persist across restart

### Installer and packaging

- [ ] MT-30 local install: one launcher, transparent icon corners, symbolic tray
- [ ] MT-31 service menu/commands: status, enable, start, stop, disable
- [ ] MT-32 uninstall stops/disables the service and keeps data/config;
      reinstall leaves one launcher

### Durability

- [ ] MT-33 hard power loss at several commit moments: `HEAD` old or complete,
      `verify` passes, GC cleans up
- [ ] MT-34 attach/detach loop: no stuck lock, duplicate sync or leak
- [ ] MT-35 lost drive recovery: rebuild from A, recover B's unsynced work
      through the preview (no deletions)
- [ ] MT-36 local changes picked up automatically within `poll_seconds`

## Open questions

1. History depth: 14/8/6 or “last 30 versions”? Drives the required drive size.
2. Auto-sync on plug (`auto_sync = true` today) or a button?
3. Transparent `current/` tree on the drive (hardlink farm, ext4 only)?
4. One drive or two for rotation?
5. Keep both `local-wins` and `newest` conflict policies, or only “keep both”?
6. Tray menu: “Quit Walky” stops the daemon and closes the UI; the window's
   Quit keeps the daemon and tray running. Is that the right split?

## Accepted decisions

- A store with a different `repo_id` is never merged silently. It enters
  `needs_recovery`; the user previews an additive merge (no deletions,
  conflicts keep both), then the config adopts the new `repo_id` and the base
  is reset. History starts fresh.
- The drive is read by Linux only → ext4, every other filesystem rejected at
  init/open (LUKS-wrapped ext4 is fine once mounted).
- Windows/macOS support is out of scope.
- LUKS is optional and never enforced, but strongly recommended: a lost or
  stolen drive without encryption exposes all data and history.
- Multiple machines → `machine-id`, per-machine base, generations.
- Conflicts → keep both variants.
- Stack → Go core/CLI + Vala/GTK4 UI over D-Bus, one `walky` binary for CLI and
  daemon, no separate recovery utility.

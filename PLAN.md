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
| M6 | Desktop UI | ✅ done (tray optional) |
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
  applies them immediately. Closing the window keeps the app alive in the
  background (Background portal), and a missing daemon is started through
  systemd on demand.
- **Packaging surface** — `install.sh` (local/system/uninstall), systemd user
  unit, `.desktop`, icon in hicolor (SVG + PNG 16–256), `VERSION`.
- **Policy** — Linux-only, ext4-only enforced at init/CLI/daemon; LUKS optional
  and never enforced, but strongly recommended; the standalone extractor was
  removed, recovery is `walky restore`.

## Remaining work

### P1 — finished

Settings, background mode, daemon autostart and udisks automount are in place.
Only manual hardware/session checks and an optional tray fallback remain.

### P1 — desktop follow-ups

- [x] Settings window with immediate apply (D-Bus `GetSettings`/`SetSettings`,
      daemon validates and rewrites the config).
- [x] Keep the app alive after the window closes (Background portal, window is
      hidden instead of destroyed).
- [x] Start `walky.service` through the user systemd manager when the daemon is
      not running.
- [ ] Optional tray icon (StatusNotifierItem) as a fallback for desktops without
      the Background portal (not required on GNOME).
- [ ] Manual checks MT-10…MT-14 and MT-20…MT-23 on a real session.

### P1 — drive lifecycle

- [x] Mount a Walky drive through udisks2 when the desktop did not automount it
      (labelled `WALKY`, `noatime`, polkit-aware, retried every 5 s).
- [ ] Do not attempt LUKS unlocking from Walky: the desktop handles the
      passphrase; Walky only waits for the ext4 mount to appear.
- [ ] Real-hardware checks MT-10…MT-14 (plug/unplug, eject, suspend, daemon
      restart).

### P2 — maintenance and long-running behaviour

- [ ] Scheduled GC: systemd user timer or a daemon timer that only runs when
      idle and not dirty.
- [ ] Prune `machines/*.json` records that have not synced for a long time, so
      GC can stop retaining their base generations.
- [ ] Rotate `log/` on the drive (currently nothing writes there yet).
- [ ] Surface GC results (freed bytes, warnings) as a daemon event for the UI.

### P2 — quality

- [ ] CLI integration tests for `cmd/walky` (currently ~1% coverage): `init`,
      `snapshot`, `ls`, `restore`, `verify`, `gc`, `trash`, `sync --direct`.
- [ ] D-Bus service test over a private bus (`dbus-run-session`).
- [ ] Fuzz the marker, manifest and pack-index parsers.
- [ ] Fault injection with `dm-flakey`/`dm-error` on loop devices (root-only,
      documented in `docs/MANUAL_TESTS.md`).

### P2 — LUKS guidance

- [ ] Non-blocking warning from `walky init` and `walky status` when the ext4
      mount is not on dm-crypt: “LUKS2 is strongly recommended”. Never refuse,
      never prompt; detection through mountinfo + sysfs.

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
- Automount only considers filesystems labelled `WALKY`; the desktop can still
  mount other drives, but Walky will not do it for them.
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
6. Commit only on explicit request.

## Open questions

1. History depth: 14/8/6 or “last 30 versions”? Drives the required drive size.
2. Auto-sync on plug (`auto_sync = true` today) or a button?
3. Transparent `current/` tree on the drive (hardlink farm, ext4 only)?
4. One drive or two for rotation?
5. Keep both `local-wins` and `newest` conflict policies, or only “keep both”?
6. Background portal vs StatusNotifierItem for the “still running” indicator?

## Accepted decisions

- The drive is read by Linux only → ext4, every other filesystem rejected at
  init/open (LUKS-wrapped ext4 is fine once mounted).
- Windows/macOS support is out of scope.
- LUKS is optional and never enforced, but strongly recommended: a lost or
  stolen drive without encryption exposes all data and history.
- Multiple machines → `machine-id`, per-machine base, generations.
- Conflicts → keep both variants.
- Stack → Go core/CLI + Vala/GTK4 UI over D-Bus, one `walky` binary for CLI and
  daemon, no separate recovery utility.

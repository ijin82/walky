# Walky — an offline "cloud" on a flash drive

Project: a daemon plus GUI for two-way synchronization of a local folder with a
store on a USB flash drive that plays the role of a Dropbox/Yandex Disk server.
No internet is required; the transport is a physical drive with a special marker.

This document records decisions, pitfalls and the work plan. Version 0.1
(discussion).

---

## 1. What we build and what this is not

We build:

- a **daemon** (`walky daemon`) running in a systemd user session that notices
  the drive, mounts it (through udisks2), synchronizes the folder and tracks
  changes with inotify plus a periodic scan;
- a **store on the drive**: content-addressed objects plus snapshots (version
  history), resistant to unplugging and able to restore onto a clean machine;
- a **CLI** (`walky init|attach|status|sync|verify|gc|restore|export`);
- a **GUI** in GTK4/libadwaita: status, progress, last 5 files, eject.

It is not:

- a backup system (the drive is the only copy until a second one exists);
- network synchronization (Syncthing/rsync over ssh) — this is sneakernet;
- a filesystem with transparent access (see the `current/` option in §4.2; by
  default the drive holds an opaque CAS store).

Key property: **the drive is the source of truth for history**, not the local
disk. The local disk can be lost; a new machine must restore from the drive.

---

## 2. Key decisions (TL;DR)

| Question | Decision | Why |
|---|---|---|
| Sync model | two-way, 3-way merge against the last common snapshot (base) | like Dropbox; without a base, "deleted" and "not yet downloaded" are indistinguishable |
| Storage | CAS: blake3 hash → object; small files packed; snapshots are manifests | deduplication across versions, free renames, crash resilience |
| Drive filesystem | **ext4 (+LUKS2) only**; other filesystems are rejected | hardlinks/symlinks/modes/journal; the store cannot degrade silently. Windows/macOS cannot mount ext4 and are out of scope |
| Detection | user daemon + udisks2 D-Bus (no root); optionally udev+systemd for headless | udev cannot start user units; udisks already mounts in GNOME |
| Core language | **Go** (single static binary, godbus, modernc/sqlite, blake3, zstd) | already using Go; the UI is thin |
| UI language | **Vala + GTK4/libadwaita** (separate process, D-Bus) | minimal code for a native GNOME look; a UI crash does not break sync |
| Alternative | everything in Rust (gtk4-rs + zbus) | if one language matters more than effort |
| GNOME integration | window + Background portal + GNotification; optional SNI tray | `appindicatorsupport` is installed, but the portal is the native path |
| Unit of history | generation + manifest, HEAD written last | atomic commits without filesystem transactions |

### 2.1 Accepted (2026-11-24)

1. The drive is read by **Linux only** → **ext4 + LUKS2**, no exFAT compromises.
2. **Multiple machines** → the full model: `machine-id`, per-machine base,
   generations, warnings about a stale base.
3. Conflicts — **keep both** variants (`name.conflict-<host>-<time>.ext`).
4. Stack — **Go core/CLI + Vala + GTK4/libadwaita UI** over D-Bus.
5. The filesystem and encryption choices mean symlinks, modes, xattrs and
   hardlinks are supported natively; metadata is still duplicated into the
   manifest (for restoring onto another machine and for `export`).

Implementation consequences: the `current/` farm becomes possible (hardlinks on
ext4), Windows compatibility is irrelevant — `renameat2`, `O_NOFOLLOW` and
directory `fsync` can be used freely. LUKS is optional and never enforced, but
strongly recommended against loss/theft: it is a container around ext4, so the
filesystem check is the same with or without encryption.

---

## 3. Pitfalls to account for

### 3.1 Data and filesystem

1. **The drive is not a backup.** It gets lost, breaks and wears out. Needed: a
   second drive (rotation), `walky verify` after writing, a UI warning when
   there is only one replica. The 3-2-1 rule still applies; an offline Dropbox
   does not replace it.
2. **ext4 only.** exFAT has no modes, symlinks, hardlinks or xattrs, is
   case-insensitive, has 10 ms mtime resolution, 32–128 KiB clusters and no
   journal; supporting it would silently degrade the store. Walky therefore
   checks the filesystem (`store.CheckFilesystem`) and refuses to initialize or
   open a store elsewhere. LUKS-wrapped ext4 is accepted once mounted. Metadata
   (mode/uid/symlink/xattr) is still recorded in the manifest so restores are
   exact.
3. **Cluster overhead and small files.** 200k files of 20 KiB on exFAT with
   128 KiB clusters waste roughly 25 GB. Solution: pack small objects into
   16–32 MiB packs. That also removes per-file `fsync`.
4. **No journal on exFAT.** Unplugging during a write leaves a dirty filesystem
   and requires fsck. Needed: `-o noatime,flush`, writes only through
   tmp+rename, an "Eject" button (`udisksctl unmount` + `power-off`) and a fsck
   check when the dirty flag is set.
5. **Atomicity.** Invariant: HEAD on the drive always points at a fully written
   manifest whose blobs are present and verified. Order: objects/packs → fsync →
   manifest (tmp→rename) → meta → `machines/<id>.json` → HEAD (tmp→rename) →
   fsync directories. A failure before HEAD leaves harmless garbage for GC.
6. **Interrupted sync.** Drive unplugged / laptop suspended / ENOSPC. The daemon
   must abort, mark the device dirty, **not** update the local base, and finish
   the commit from the start on the next attach. Local temporary files are
   removed on startup (intent journal).
7. **Sleep/hibernation.** Subscribe to `org.freedesktop.login1`
   `PrepareForSleep`: stop writing and fsync before suspend. Otherwise USB may
   disappear in the middle of a pack.
8. **USB autosuspend.** Cheap drives disconnect under `usbcore.autosuspend`: a
   udev rule with `ATTR{power/control}="on"` for that model plus
   `queue/scheduler=none`.
9. **Case and Unicode.** exFAT is case-insensitive → `README` and `readme`
   collide. macOS produces NFD, Linux NFC → collisions on exFAT. Compare paths
   in NFC and fail loudly when applying to a case-insensitive filesystem instead
   of silently overwriting.
10. **Special files.** Symlinks (cannot be materialized on exFAT), hardlinks
    (duplicate, though CAS dedup makes it free on the drive), sparse files
    (store the hole map), FIFOs/sockets/devices (skip with a log entry).
11. **Git repositories.** `.git` changes constantly: packfiles are rewritten by
    `git gc`, `*.lock` files appear. Syncing on the fly can capture a state where
    a ref points at an object that was not written yet. Practice: if the tree
    contains `*.lock`, delay the sync by 30 s; never sync `.git/index.lock`;
    remember that a snapshot is atomic per file, not per repository (rollback is
    always possible).
12. **Project junk.** `node_modules`, `target/`, `build/`, `.venv`,
    `__pycache__`, `.cache`, `*.iso` turn 15 GB into 150 GB. Ship a default
    exclude list plus `.walkyignore` in gitignore style.
13. **mtime is untrusted.** Resolution of 2 s/10 ms, time zones, different
    machines. Compare only (size, hash); use mtime as a cache heuristic.

### 3.2 Synchronization

14. **Three states, not two.** A base is required: the manifest of this
    machine's last successful sync. Otherwise a deletion cannot be distinguished
    from "the file appeared on another machine".
15. **Tombstones.** A deletion must be an explicit event with a generation, or
    an old machine will resurrect the file. Keep them for N days; GC only once
    every machine has moved past them.
16. **Conflicts.** Both sides changed → never overwrite. Default policy: keep
    both (`name.conflict-<host>-<timestamp>.ext`), the original chosen by
    "newest" or "local" policy. Deletion vs modification → modification wins
    (data matters more).
17. **Multiple machines.** The drive is transport and shared history. Each
    machine has its own base (`machines/<id>.json` on the drive plus a local
    copy). Generations form a chain; HEAD moves on every commit. A machine that
    has not synced for longer than retention gets a warning on attach and
    requires an explicit decision.
18. **Free space.** Preflight: estimate new objects + manifest + 10%. Do not
    start when it does not fit; show a clear UI error. On ENOSPC abort cleanly
    and do not move HEAD.
19. **GC.** Mark-and-sweep from every retained manifest and every
    `machines/*.json`. Never delete blobs referenced by another machine. Run on
    a schedule or when space is low, never during a sync.
20. **Renames.** Hash dedup makes them free in traffic; history can detect them
    through the inode cache, but that is not required for the MVP.
21. **Single writer.** `flock` on `/WALKY/lock` plus pid/hostname/boot_id. A
    second instance (or a second machine, if the drive is plugged into both) is
    rejected with a message.

### 3.3 Security and integrity

22. **blake3 per object**, verified on write and on read. `walky verify` (full)
    and a quick `--quick` (HEAD + latest commit) after attaching.
23. **Drive spoofing.** The marker file can be forged: keep a `repo_id`
    allowlist in the config, check `fs_uuid`, optionally sign the manifest
    (ed25519).
24. **Path traversal on restore.** Relative paths only, no `..`, no absolute
    paths, no symlink traversal. In Go — `os.Root` or
    `openat2(RESOLVE_BENEATH)`; in Rust — `cap-std`.
25. **Encryption.** LUKS2 over the whole drive (simple and transparent) with a
    keyfile in `~/.config/walky/` plus a passphrase fallback; or app-level (age)
    at the cost of Windows compatibility. Without encryption, losing the drive
    leaks source code.
26. **Ownership on restore.** uid/gid may not match; the default is "restore as
    the current user", `--preserve-owner` is an option.
27. **Logs on the drive** must be rotated (5 files of 1 MiB), or the log eats
    space.

### 3.4 Operations

28. **inotify.** `max_user_watches` (often 8192) — 20k directories need 524288.
    Overflow (`IN_Q_OVERFLOW`) triggers a full rescan. Watch directories, not
    files.
29. **Scan cost.** 200k files: a warm scan from the stat cache is ~1–3 s, a cold
    hash of 15 GB is ~10–30 s (blake3, NVMe). Parallelize carefully: 2–4 workers
    on an HDD, more on NVMe.
30. **USB speed.** USB 2.0: 15–25 MB/s → the first 15 GB takes ~10–17 min.
    USB 3.0: 50–150 MB/s → 2–5 min. Increments take seconds. So "not slower than
    the internet" holds for increments and is roughly true for the first upload
    on USB 3. The bottleneck is not bandwidth but `fsync` and small files →
    packs.
31. **Flash wear.** Cheap TLC/QLC drives die from rewrites; packs are
    append-only, old ones are not rewritten, but every version writes new packs.
    For 15 GB with daily increments, 128 GB lasts a long time; 32 GB is tight.
32. **Drive size.** 15 GB of data plus history (dedup usually adds 20–40%) →
    64 GB minimum, 128 GB comfortable.
33. **Format compatibility.** `format: 1` in the marker, version checks,
    migrations. An old daemon must not write a new format.
34. **Single instance and autostart.** D-Bus activation of a user unit,
    `Type=dbus`, `Restart=on-failure`; the GUI starts the daemon on demand.

---

## 4. Architecture

### 4.1 Components

```
┌─────────────────────────────┐        D-Bus io.github.ijin.Walky1
│ walky daemon (Go)           │◄──────────────────────────────┐
│  device  ← mount table      │                               │
│  scan    → SQLite index     │        ┌──────────────────────┴─────┐
│  store   → CAS/packs        │        │ walky-ui (Vala, GTK4/Adw)  │
│  sync    → 3-way merge      │        │  status, progress, last 5  │
│  api     → D-Bus server     │        │  settings, eject           │
└──────────┬──────────────────┘        └────────────────────────────┘
           │ mount /run/media/$USER/WALKY
           ▼
      USB flash (ext4+LUKS)
```

The `walky` CLI is the same binary as the daemon (`walky daemon` /
`walky status`); it talks to the daemon over D-Bus and, with `--direct`, works
with the store directly (for recovery on a live system and for tests).

### 4.2 On-drive store format

```
/WALKY/
  walky.json                  # marker: format, repo_id, fs_uuid, label, created
  HEAD                        # {"gen":42,"manifest_sha256":"..."} — written last
  objects/ab/cd/<blake3>      # objects larger than 1 MiB, one file each
  packs/pack-<id>.pack        # small objects, 32 MiB target, one zstd frame each
  packs/pack-<id>.idx         # sorted (hash, offset, compLen, rawLen) for lookups
  snapshots/00000042.jsonl.zst# full manifest of the generation
  snapshots/00000042.meta.json# machine, parent, time, stats, signature (opt.)
  machines/<machine-id>.json  # this machine's base_gen, hostname, last_sync
  trash/<gen>.json            # entries deleted by that generation (retention)
  log/walky-YYYYMMDD.log
  lock                        # flock
```

The manifest is JSONL, sorted by path, compressed with zstd:

```json
{"p":"docs","t":"d","m":493}
{"p":"src/main.go","t":"f","s":1234,"m":420,"h":"9f2c…","mt":1732456789123456789}
{"p":"link","t":"l","tg":"../x"}
{"p":"old.txt","t":"x"}
```

`t`: `f` file, `d` directory, `l` symlink, `x` tombstone.
Large files use `b` (a list of chunks, FastCDC, 1–4 MiB average) to deduplicate
between versions of images/archives; everything else uses `h`, the hash of the
whole file.

A full manifest for 200k files is ~20–25 MB of JSON → 5–8 MB with zstd. With a
retention of ~40 generations that is 200–300 MB, which is acceptable; switching
to deltas against the parent (a full manifest every 10 generations) is a later
optimization.

**Optional human-readable `current/`** (ext4 only): a hardlink farm pointing at
the latest snapshot so the drive can be read without walky. Downside: editing a
file on the drive corrupts the blob (same inode) — mitigated by `chmod 0444` and
`verify`/repair, but it is extra risk. Therefore: the MVP ships
`walky export <gen> <dir>`; `current/` comes later and stays optional.

### 4.3 Local state

```
~/.config/walky/config.toml      # settings
~/.local/state/walky/
  index-<repo>.db                # SQLite: path, dev, ino, size, mtime, ctime, hash
  base-<repo>.jsonl.zst          # base manifest of the last successful sync
  base-<repo>.jsonl.zst.meta.json# generation and timestamp of that base
  trash/<timestamp>/<path>       # files deleted locally by a sync
```

Config (TOML):

```toml
[repo]
path = "~/Sync"
repo_id = "…"            # allowlist, guards against a foreign drive with a copied marker

[sync]
mode = "two-way"         # two-way | push-only
conflict = "copy"        # copy | newest | local-wins
debounce_ms = 3000
full_scan_min = 10

[exclude]
patterns = ["**/node_modules", "**/target", "**/.venv", "**/__pycache__",
            "**/.cache", "**/*.lock", "**/*.tmp"]
max_file_size = "8G"

[retention]
daily = 14
weekly = 8
monthly = 6
trash_days = 30

[device]
auto_sync = true
eject_after_sync = false
```

### 4.4 Sync algorithm

Inputs: `L` — the local manifest (after the scan), `S` — the HEAD manifest on
the drive, `B` — this machine's base. For every path:

| L vs B | S vs B | Action |
|---|---|---|
| = | = | nothing |
| changed | = | **push** |
| = | changed | **pull** |
| changed | changed, L == S | converge, update base |
| changed | changed, L != S | **conflict** |
| deleted | = | delete on the drive (entry recorded in `trash/<gen>.json`) |
| = | deleted | delete locally (into the local trash) |
| deleted | changed | conflict: resurrect the modified version |

Phase order:

1. Rescan the local folder (inotify events plus a full scan on a timer).
2. Validate the drive: marker, `repo_id`, format version, fsck flag, free space.
3. Compute the plan (push/pull/conflict) and its size.
4. **Pull phase**: download missing objects, apply them atomically (tmp in the
   same directory → fsync → `renameat2(RENAME_NOREPLACE)` → fsync the directory),
   restore mode/mtime/symlink.
5. **Push phase**: write new objects/packs (tmp → fsync → rename), then commit
   the manifest per §4.2, update `machines/<id>.json`, then HEAD.
6. Update the local `base` **only after** a successful HEAD and all applied
   pulls.
7. Emit UI events (signals), write the log, optionally eject.

Why pull before push: if the push fails, the local base has not moved while the
downloaded files are already applied — a safe state that is simply recomputed on
the next run.

Implementation notes (M4): the drive manifest remains a full tree, so a deletion
is inferred by comparing the local or drive tree with the base instead of being
stored as a tombstone entry. This is correct for the supported scenarios and
keeps manifests small; explicit tombstones are a possible later addition for
machines whose base predates the trash retention window. The commit is
conditional on the generation the merge was computed from (`ExpectParent`), so a
concurrent writer cannot be silently overwritten. Conflict copies are ordinary
files and are pushed like any other change.

### 4.5 Durability invariants

- **I1.** HEAD is always consistent: the manifest exists, its sha256 matches,
  and every referenced object exists.
- **I2.** The base does not move until the commit on the drive is complete.
- **I3.** No local file is deleted without a copy in the trash (local or on the
  drive).
- **I4.** An interruption at any point leaves the next run able to roll back
  garbage or finish the commit; there is no half-written HEAD.
- **I5.** Reads always go through HEAD; extra objects are ignored and collected
  by GC.

Test I1–I4 with fault injection: `SIGKILL` at every protocol point (failpoints),
`dmsetup` `flakey`/`error` targets on a loop device, and unplugging the loop
device.

### 4.6 Conflicts

- Default: both variants are kept, the local one is renamed to
  `<name>.conflict-<host>-<YYYYMMDD-HHMMSS>.<ext>`; the UI shows a conflict
  event.
- Options: `newest` (by mtime, with the untrusted-clock caveat), `local-wins`.
- Deletion vs modification → the modification wins, in the log and the UI.
- Conflicts never block the rest of the sync.

### 4.7 Retention and GC

- Policy: 14 daily + 8 weekly + 6 monthly (configurable) plus HEAD and the base
  generation of every machine in `machines/`; 30-day trash.
- Mark: every kept manifest and every non-expired `trash/<gen>.json` entry.
- Sweep: unreferenced standalone objects are deleted; packs with only dead
  entries are removed; mixed packs are rewritten into fresh packs (live objects
  already present standalone or in retained packs are not duplicated).
  Compaction requires free space and otherwise falls back to removing only dead
  packs.
- Order: objects and packs first, generation files last, so a crash never leaves
  a manifest pointing at missing data.
- Trigger: manually with `walky gc`, or on a schedule once the daemon grows a
  timer; never while dirty and never without HEAD.

---

## 5. Drive detection and systemd

### 5.1 Recommended: user daemon + udisks2

- The daemon polls the mount table (`/proc/self/mountinfo`, 2 s by default),
  accepts mounts that contain a valid `walky.json` (either `<mount>/WALKY/` or
  the mount root), and validates `repo_id` against the config. This needs no
  root and no polkit, and works the same whether udisks or the user mounted the
  drive.
- udisks2 is still used for `Filesystem.Unmount` and `Drive.PowerOff` on eject.
- If a filesystem labelled `WALKY` exists but is not mounted, the daemon asks
  udisks2 to mount it (`auto_mount`, checked every 5 s, option `noatime`).
  LUKS unlocking is left to the desktop; Walky only waits for the ext4 mount.
- Pros: no root, works inside the session, can show UI, can unmount cleanly.
- Con: does not work without a login (for headless, see §5.2).

`~/.config/systemd/user/walky.service`:

```ini
[Unit]
Description=Walky offline drive sync
Documentation=man:walky(1)
PartOf=graphical-session.target
After=graphical-session.target
StartLimitIntervalSec=0

[Service]
Type=dbus
BusName=io.github.ijin.Walky1
ExecStart=/usr/bin/walky daemon
Restart=on-failure
RestartSec=3
Slice=background.slice
NoNewPrivileges=yes
PrivateTmp=yes
ProtectControlGroups=yes
ProtectKernelModules=yes

[Install]
WantedBy=graphical-session.target
```

`Type=dbus` plus `BusName` enables activation on demand: the GUI/CLI simply call
a method and systemd starts the daemon.

### 5.2 Headless / no login

udev cannot start user units, so the system-wide mode uses:

```udev
ACTION=="add", SUBSYSTEM=="block", ENV{ID_FS_LABEL}=="WALKY", \
  TAG+="systemd", ENV{SYSTEMD_WANTS}+="walky@%k.service"
```

`walky@.service` is a system template with `User=` and
`ExecStart=/usr/bin/walky daemon --device /dev/%i`. The marker must still be
validated inside the daemon (labels can be forged).

### 5.3 Lifecycle

- Start: scan already-mounted devices (the drive may have been plugged in before
  login or before the daemon started).
- Attach: wait for the mount to settle (retry reading the marker 5×200 ms), run
  `walky verify --quick`, then auto-sync if enabled.
- Detach: abort the current commit, do not move the base, mark the device as
  "unsafely removed".
- Eject button: wait for the commit to finish → `Filesystem.Unmount` →
  `Drive.PowerOff` → "safe to remove" notification.
- Sleep: `PrepareForSleep` → pause and fsync.

---

## 6. UI in GNOME

Implementation note (M6): the window polls `GetStatus` and `GetEvents` once per
second instead of subscribing to signals, which keeps the Vala client small and
avoids missed-event state. Settings, the Background portal and the tray icon are
still to be added.

### 6.1 Window (GTK4 + libadwaita 1.8)

- `Adw.ApplicationWindow` + `Adw.ToolbarView`, `Adw.HeaderBar` with
  `Adw.WindowTitle` (title "Walky", subtitle the state: "Synced 5 minutes ago",
  "Syncing: 37/120").
- `Adw.Banner` for errors (no space, conflict, unsafe removal).
- Device card: label, UUID, `Gtk.LevelBar` for free space, "Eject" button,
  "Sync automatically" switch.
- Progress: `Gtk.ProgressBar` (pulse when the total is unknown), throughput,
  ETA.
- "Last 5 files": `Gtk.ListBox` of `Adw.ActionRow` (icon, name, ↑/↓, size,
  "2 minutes ago"); a "All events" button opens a filterable dialog.
- Hamburger menu: "Sync now", "Pause", "Verify integrity", "Settings"
  (`Adw.PreferencesWindow`: excludes, retention, conflicts), "Log", "About".
- `Adw.ToastOverlay` toast plus `Gio.Notification` on completion and errors.

### 6.2 "Visible as running"

Two independent mechanisms; ideally both:

1. **Background portal** (`org.freedesktop.portal.Background.RequestBackground`,
   `reason`, autostart) → GNOME 45+ lists the app in Quick Settings
   ("Background Apps") where it can be terminated. This is the native path.
2. **StatusNotifierItem** (tray) — this machine already has
   `appindicatorsupport@rgcjonas.gmail.com`, so an icon with a menu (status,
   recent files, "Sync", "Show window") will be visible. It can be implemented
   directly in Go through `org.kde.StatusNotifierItem` +
   `com.canonical.dbusmenu`, without GTK3 dependencies.

Plus a `.desktop` file with `X-GNOME-UsesNotifications=true`,
`DBusActivatable=true`, an icon and `Keywords`.

### 6.3 D-Bus contract

```xml
<node>
  <interface name="io.github.ijin.Walky1">
    <method name="SyncNow"><arg type="b" direction="in" name="wait"/></method>
    <method name="Pause"><arg type="b" direction="in" name="paused"/></method>
    <method name="GetStatus"><arg type="a{sv}" direction="out" name="status"/></method>
    <method name="GetEvents"><arg type="u" direction="in" name="limit"/>
      <arg type="a(ssstt)" direction="out" name="events"/></method>
    <method name="Eject"/>
    <method name="Verify"><arg type="b" direction="in" name="full"/></method>
    <signal name="StateChanged"><arg type="s" name="state"/></signal>
    <signal name="Progress"><arg type="u" name="done"/><arg type="u" name="total"/>
      <arg type="t" name="bytes"/></signal>
    <signal name="FileDone"><arg type="s" name="path"/><arg type="s" name="op"/>
      <arg type="s" name="result"/></signal>
  </interface>
</node>
```

`GetStatus` returns `state`, `device`, `last_sync`, `pending`, `conflicts`,
`free_bytes`, `version`. Throttle signals (≤10/s). The UI never blocks: all calls
are asynchronous and progress arrives through signals.

---

## 7. Language choice

| | Go | Rust | Vala |
|---|---|---|---|
| Sync core | ★★★★★ (goroutines, static binary, `godbus`, `modernc/sqlite`, `blake3`, `klauspost/zstd`) | ★★★★★ (`zbus`, `rusqlite`, `notify`, `serde`) | ★★ (few libraries; a GC runtime is fine for I/O, but the ecosystem is thin) |
| GTK4/Adw | ★★ (`gotk4`: cgo, GObject lifetimes, packaging) | ★★★★ (`gtk4-rs`, `libadwaita`) | ★★★★★ (native, minimal code) |
| Development speed | ★★★★★ | ★★★ | ★★★★ |
| Reliability/tests | ★★★★ | ★★★★★ | ★★★ |
| One language for everything | ★★★ (UI is the weak spot) | ★★★★★ | ★★★★ (core is the weak spot) |
| Binary/dependencies | one static binary | one static binary | glib/gtk from the system |

**Recommendation:** core and CLI in Go; UI in Vala (GTK4/libadwaita) as a
separate process over D-Bus. This gives a fast start (already using Go), a
native GNOME look and the "UI crashed, sync keeps running" separation. If one
language matters and there is time, all-Rust (`gtk4-rs` + `zbus` + `notify` +
`rusqlite`) is the cleanest monolith. A Vala monolith is not recommended: a sync
engine in Vala would be painful (hashes, zstd, SQLite, tests, concurrency).

Repository structure:

```
core/                 # Go module "walky", built independently
  cmd/walky/          # main, CLI, daemon
  internal/store/     # CAS, packs, manifests, commit, GC
  internal/scan/      # tree walk, stat cache, hashing, excludes
  internal/sync/      # 3-way merge, apply, conflicts
  internal/device/    # udisks2, mounting, ejection
  internal/index/     # SQLite hash cache
  internal/api/       # D-Bus server + SNI
  internal/config/    # config, validation
ui/                   # Vala + GTK4/libadwaita, separate process, D-Bus
  src/                # Application.vala, Window.vala
  Makefile            # valac, no meson
data/                 # systemd units, .desktop, icons, udev, sysctl
docs/
```

Boundaries: `core/` knows nothing about GTK, `ui/` knows nothing about the
on-drive format — only the D-Bus contract in `internal/api/walky1.xml`. This
makes the engine testable without a display and lets the UI change without
touching synchronization.

---

## 8. Work plan

Live status, checklists and acceptance criteria are in [PLAN.md](../PLAN.md).
The table below is the original size estimate; it is not updated as work
progresses.

Estimates assume one developer part-time; full-time is roughly twice as fast.

| Milestone | Scope | DoD | Estimate |
|---|---|---|---|
| **M0. Skeleton** | repository, `go.mod`, CLI skeleton, config, logging, CI, D-Bus draft, hello window in Vala | `walky init` creates the layout on a loop device; `go test ./...` green | 0.5–1 d |
| **M1. Storage** | CAS, manifests, commit protocol, `snapshot/restore/verify/ls/export` | 15 GB: snapshot → byte-identical restore (hash tree), modes/symlinks/mtime preserved; SIGKILL at every point does not break HEAD | 3–4 d |
| **M2. Scanner** | SQLite cache, parallel blake3, excludes/gitignore, packs | warm scan of 200k files < 5 s; cold hash of 15 GB < 60 s; exclude tests | 2–3 d |
| **M3. Daemon** | udisks2, mounting, marker, user unit, D-Bus API, events, singleton | 50 plug/unplug cycles without failures; auto-sync; no root; daemon restart mid-sync | 2–3 d |
| **M4. Two-way sync** | 3-way, tombstones, conflicts, per-machine base, atomic apply, resume, space | property test: 2 "machines" + loop drive, 10k random operations, zero loss; correct conflicts | 5–7 d |
| **M5. Retention/GC** | policy, mark-sweep, trash, `walky gc` | after 100 generations GC leaves no dangling references, `verify` clean | 2–3 d |
| **M6. UI** | window, status, progress, last 5, device, settings, notifications, portal/tray, .desktop | manual UX scenario; UI starts the daemon; nothing blocks | 4–6 d |
| **M7. Packaging** | Makefile, rpm (ALT), install/uninstall, man, README, recovery guide, inotify sysctl | clean install on a VM | 2–3 d |
| **M8. Hardening (optional)** | optional mkfs/LUKS helper, manifest signing, `dm-flakey`, manifest parser fuzz, 24-hour soak | 100 fault injections without data loss | 3–5 d |

**Done:** M0 (skeleton), M1 (storage), M2 (index + packs), M3 (daemon) and M4
(two-way sync) — see `core/internal/{store,index,scan,engine,daemon,device,api}`:
`Init/Open`, blake3 CAS, generation manifests, the commit protocol with HEAD
last, `Restore` through `os.Root` with hash verification while copying,
`Verify`, failpoint tests for commits, the SQLite hash cache and 32 MiB packs
with a binary `.idx`, mount-table detection with marker validation, the D-Bus
service, per-machine bases, conflict copies and conditional commits.
`walky init` only initializes a **mounted** directory and never formats a
device. A disk-formatting helper is optional future work in M8 (deliberately,
to avoid wiping a disk by mistake); encryption would require a LUKS container
but is not mandatory.

**MVP in a week or two:** M0–M3 in push-only mode (snapshots plus restore) is
already a useful "history on the drive". Two-way sync is M4.

Total: ~4–6 weeks part-time, ~2–3 weeks full-time up to M7.

---

## 9. Testing and metrics

- **Unit/integration:** a loop-backed ext4 image plus `udisksctl loop-setup`;
  two "clients" with different `machine-id` values sharing a loop store.
- **Fault injection:** failpoints in the commit protocol plus `SIGKILL`;
  `dmsetup` `error` and `flakey` targets; disconnecting the loop device during a
  write.
- **Property test:** random create/write/delete operations on two simulated
  machines sharing one store, checking convergence and store integrity. The
  current test runs 2000 operations; scaling it to 10k is a matter of runtime.
- **Fuzz:** parsers for `walky.json`, `.meta.json`, manifests and pack indexes.
- **Metrics:** first 15 GB sync (USB2/USB3), a 100-file increment, a warm scan
  of 200k files, daemon RSS, idle CPU < 1%, zero data loss.
- **Scenarios:** unplug during push; during pull; no space left; a foreign drive
  with the same label; two instances; sleep in the middle of a commit; a
  read-only drive.

---

## 10. Why not use an existing tool

| Tool | What it does | Why it does not fit |
|---|---|---|
| Syncthing | P2P sync, versions, conflicts | needs a network and always-on nodes; no "flash drive" transport |
| Unison | two-way sync with an archive | no history/dedup, no UI, fragile on large trees |
| `rsync --link-dest` | snapshots on ext4 | one-way, no merge/conflicts, hardlinks only on ext4 |
| restic / borg | dedup, encryption, versions | backup, not two-way sync; no base/merge |
| git-annex | literally sneakernet, dedup | symlink/annex UX, requires git, slow on 200k files |
| Dropbox/Yandex | everything | there is no internet |

Conclusion: the "3-way merge + CAS + flash-drive transport" logic is unique, but
the format ideas (packs, FastCDC, manifests, GC) should be borrowed from
restic/borg, and the conflict model from Syncthing/Unison.

---

## 11. Open questions

Decided: filesystem (ext4+LUKS2), multiple machines, conflicts "keep both",
stack Go+Vala. Still to settle before M5–M6:

1. **History depth.** 14 daily + 8 weekly + 6 monthly — or "last 30 versions" to
   save space? This drives the drive size (64 vs 128 GB).
2. **Auto-sync on attach or a button?** The config currently has
   `auto_sync = true` plus a notification; eject is manual.
3. **Do we need a transparent `current/` on the drive?** It is possible now
   (ext4, hardlinks), but editing a file on the drive corrupts the blob — it
   needs `chmod 0444` and repair during verify.
4. **One drive or two (rotation)?** For real safety, two; the second replica
   does not need to be permanently attached.
5. **LUKS: keyfile + passphrase or passphrase only?** A keyfile is more
   convenient (auto-unlock) but lives on the same machine; TPM
   (`systemd-cryptenroll`) is a compromise.

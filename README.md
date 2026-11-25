# Walky — an offline "cloud" on a USB drive

![Aster](data/walky.svg)

Two-way synchronization of a folder with a USB flash drive that acts as the
remote server of Dropbox/Yandex Disk: version history, deduplication, conflict
handling and restore onto a new machine. No internet is required — the transport
is physical.

Status: **M0–M6**. Storage, daemon, two-way sync, GC, manual trash management
and a GTK4 window work: `init`, `snapshot`, `ls`, `head`, `restore`, `verify`,
`daemon`, `status`, `sync`, `eject`, `gc`, `trash` — blake3 CAS, generations,
atomic commits, packs, drive detection, conflict handling and a desktop UI.
Settings and a tray indicator are in place; distribution packaging is deferred.
Architecture — [docs/DESIGN.md](docs/DESIGN.md), status and plan —
[PLAN.md](PLAN.md).

## Structure

```
core/              Go module "walky": daemon, CLI, sync engine
  cmd/walky/       entry point and subcommands
  internal/        store, scan, index, engine, device, daemon, api,
                   state, config, machineid, version
ui/                Vala + GTK4/libadwaita: separate process talking over D-Bus
  src/             Application.vala, Window.vala, main.vala
data/              systemd unit, udev, sysctl, .desktop, walky.svg, example config
docs/              DESIGN.md — architecture and pitfalls
                   MANUAL_TESTS.md — hardware and session checks
PLAN.md            milestones, status, next tasks
AGENTS.md          conventions for AI assistants
VERSION            current semver, injected at build time
```

Convention: **everything inside the repository — code, identifiers, comments,
documentation and messages — is written in English**; only live conversation
with the user is in Russian. Go and Vala never share a directory and are built
independently (`core/` uses `go build`, `ui/` uses `valac`).

## Build

```sh
make check          # gofmt + go vet + go test + build both binaries
make cover          # per-package test coverage
make build          # bin/walky, bin/walky-ui
./install.sh        # build and install for the current user
make run-ui         # start the window
./bin/walky version
./bin/walky status --json
```

Requires Go ≥ 1.26 (`os.Root` with MkdirAll/Chtimes, `log/slog`) and Vala with
GTK4/libadwaita vapi files. Meson is not needed: the UI builds through a short
[ui/Makefile](ui/Makefile).

`make install-local` installs fresh binaries into `~/.local/bin` and a launcher
into `~/.local/share/applications`, so the current build is what runs, without
sudo. The version comes from [VERSION](VERSION).

## Which filesystem?

**Only ext4 is supported.** Walky checks the filesystem and refuses to
initialize or open a store on anything else, because the store relies on POSIX
permissions, symlinks and atomic renames. LUKS-encrypted ext4 works once the
container is unlocked and mounted.

Walky itself never formats a drive. If the flash drive currently uses another
filesystem, reformat it to ext4 yourself (`mkfs.ext4 -L WALKY /dev/sdX`) and
mount it again. A convenience helper for blank drives may arrive later, but it
is optional and never automatic.

The filesystem label is **only** used by the automount fallback; it is not the
store marker. The marker is `<mount>/WALKY/walky.json` (or `<mount>/walky.json`),
and any mounted drive with it is detected regardless of the label. Set the label
in the config to match an existing drive:

```toml
[device]
label = "ijin_c"
```

GNOME normally mounts removable drives itself. If nothing mounts the drive, the
daemon asks udisks2 to mount any unmounted filesystem with that label
(`auto_mount = true`, on by default). When the desktop already mounted the
drive, no label or `e2label` step is needed. LUKS is not unlocked by Walky: the
desktop asks for the passphrase and the ext4 filesystem appears afterwards.
`walky init` warns when the filesystem is not inside LUKS; it is advisory and
never blocks initialization.

### Using the drive as normal storage

The drive is a normal ext4 filesystem (inside LUKS). Browse and copy files with
Files/Nautilus, `cp`, or any other tool. Walky owns only the `/WALKY` directory:
do not move, edit or delete its files by hand. Everything else on the drive is
untouched by synchronization and is not part of the history.

To use the drive without starting a sync, pause the daemon first (window,
tray, or `walky pause`), then mount and copy files. The next sync still only
looks at the configured local folder and `/WALKY`.

### Safe removal

The preferred way is the Walky eject action (window, tray, or CLI). It commits
any running sync, unmounts the filesystem and powers the drive off:

```sh
walky eject
```

GNOME Files' eject button does the same. By hand:

```sh
# find the mapper name with lsblk, then:
udisksctl unmount -b /dev/mapper/luks-<uuid>
udisksctl lock -b /dev/sdd1        # optional, closes the LUKS mapping
udisksctl power-off -b /dev/sdd    # optional, powers the drive off
```

After the filesystem is unmounted it is safe to unplug the drive. Stop the
daemon first only if it is not responding to eject:
`systemctl --user stop walky.service`.

## Quick start (manual, no daemon)

```sh
# the drive is already mounted
./bin/walky init /run/media/$USER/WALKY
# or initialize and configure the folder in one step
./bin/walky init /run/media/$USER/WALKY --repo ~/Sync
./bin/walky snapshot --store /run/media/$USER/WALKY --repo ~/Sync
./bin/walky ls --store /run/media/$USER/WALKY --long
./bin/walky verify --store /run/media/$USER/WALKY --full

# restore on another machine (or a single file with --path)
./bin/walky restore --store /run/media/$USER/WALKY --dest ~/Restored
./bin/walky restore --store /run/media/$USER/WALKY --gen 5 --path docs/note.txt --dest ~/Restored
```

`--store` is the store directory on the drive, `--repo` is the synchronized
folder. `init` creates the directory if needed, requires the mount to be ext4
and never formats a device, so a disk cannot be wiped by accident.

Current limitations: only ext4 drives are accepted; packaging is deferred;
auto-mounting through udisks2 without a polkit prompt is a follow-up.

A repeated snapshot does not rehash the tree: the cache in
`~/.local/state/walky` compares `(dev, ino, size, mtime, ctime)`. To reset it,
use `--force-rescan`.

Measurements on 20k files: first snapshot ~1.2 s, repeated ~0.2 s, increment of
100 files ~0.6 s. On 200k files a scan takes 2.3 s cold and 1.2 s warm (details
in [PLAN.md](PLAN.md)).

## Two-way sync (M4)

```sh
# apply changes in both directions without the daemon
./bin/walky sync --direct --store /run/media/$USER/WALKY --repo ~/Sync
```

Each machine keeps a base manifest in `~/.local/state/walky`; the drive stores
per-machine state under `machines/`. Changes are merged per path: local-only
changes are pushed, drive-only changes pulled, deletions propagate, and when
both sides changed the same file a conflict copy
(`name.conflict-<host>-<time>.ext`) is created and pushed. Deleted files go to
`~/.local/state/walky/trash` on the machine and are listed in `trash/<gen>.json`
on the drive. The commit is conditional on the generation the merge started
from, so a concurrent writer is reported instead of overwritten.

## Lost or failed drive

The drive is the only copy of the version history. Current files also exist on
every machine, but old generations, the drive trash and other machines' records
exist only on the drive. If it is lost, the history is gone; the current
content can be rebuilt into a new store.

Before doing anything, preserve the unsynced work:

1. Stop the daemon on both machines (`systemctl --user stop walky.service`).
2. Copy the local sync folder somewhere safe on both machines.
3. Copy `~/.local/state/walky` (base manifest, index, trash) and
   `~/.config/walky/config.toml` from both machines.

Then rebuild the portable store from the machine with the most complete data
(call it A):

1. Use a new ext4 drive (LUKS recommended) and mount it.
2. On A, `walky init /run/media/$USER/<label>` (or **Set up a drive…** in the
   GUI). This creates a new `repo_id` and an empty history.
3. A's window shows **Recovery required**. Open **Recover drive…**, check the
   preview and merge. Recovery is additive: A's current files are uploaded and
   nothing is deleted.

Finally merge the second machine B:

1. Attach the new drive to B and open **Set up a drive…**.
2. Choose B's existing sync folder and press **Use this drive**. Because the
   drive belongs to a different store, Walky does not touch the config yet; it
   shows **Recovery required**.
3. Open **Recover drive…**, review the preview and merge.

Recovery semantics:

- files only on the machine are uploaded, files only in the store are
  downloaded;
- differing files with the same name keep both variants as conflict copies;
- **nothing is deleted**, so deletions made before the loss may come back;
- after the merge the config points at the new `repo_id`, the base is reset and
  normal two-way sync resumes.

The new store starts with a new history: old generations and the old drive trash
are not recoverable. Keep a cold copy of the drive (unmounted `dd` or `rsync` to
a second drive) if the history matters, and test restoring that copy.

## UI (M6)

```sh
make run-ui          # or ~/.local/bin/walky-ui
```

The window shows daemon state, device (with LUKS encryption status), local sync
folder (with a confirmed **Change…** button), generation (with **History…** to
browse past snapshots and restore individual files), last sync, free space, live
progress and the last five distinct files (persisted across restarts), with
buttons for sync, pause and eject. It polls the daemon over D-Bus once per
second and never writes to the store itself.

The same window has **Set up a drive…**: it lists mounted removable ext4
filesystems, shows whether a store already exists, lets you pick the local
folder and initializes the store in one click. It uses the daemon, so the same
`internal/setup` code runs for the GUI and `walky init`.

Default excludes cover `node_modules`, `vendor` (PHP Composer and similar
dependency trees, also Go `vendor/`), `target`, `build`, virtualenvs, caches and
lock/temp files. They are edited in Settings; `**/vendor` can be removed if a
project needs its vendored dependencies backed up.

Settings (excludes, retention, conflict policy and the change-poll interval) are
edited in the window and
sent to the daemon, which validates, saves and applies them immediately; the UI
never writes TOML itself. Note that saving rewrites the config file and drops
comments.

Closing the window hides it; the app stays alive and a tray icon remains in the
top bar. The icon belongs to the daemon (StatusNotifierItem), so it is visible
even when the window is closed and survives UI restarts. It uses monochrome
symbolic icons (`walky-symbolic` when connected, `walky-paused-symbolic` when
paused, `walky-offline-symbolic` when no drive is attached) so GNOME recolors
them for light and dark panels. It needs the “AppIndicator and
KStatusNotifierItem Support” GNOME extension (already present here). The menu
offers Sync now, Pause/Resume, Open Walky, Eject drive and Quit. The Background
portal is no longer used.

If the daemon is not running, the UI asks the user systemd manager to start
`walky.service` once and keeps polling.

## Trash

```sh
./bin/walky trash list --store /run/media/$USER/WALKY
./bin/walky trash empty --store /run/media/$USER/WALKY   # drive trash + GC
./bin/walky trash empty --local                          # machine trash
./bin/walky trash empty --store … --dry-run
```

Clearing the drive trash also runs GC with the configured retention, so the
space taken by deleted content is reclaimed immediately.

## Retention and GC (M5)

```sh
./bin/walky gc --store /run/media/$USER/WALKY --dry-run
./bin/walky gc --store /run/media/$USER/WALKY
```

Keeps HEAD, the requested daily/weekly/monthly generations, every active
machine's base and non-expired trash; removes unreferenced objects, deletes dead
packs and compacts mixed ones. Machine records that have not synced for
`machine_days` (180 by default) are pruned together with their bases. Deleted
content stays reachable through the trash for `trash_days` (30 by default), so
objects referenced only by trash survive until it expires. The daemon runs GC
automatically every `maintenance.gc_interval_hours` (24 by default) while it is
idle; `--dry-run` only reports.

## Daemon (M3)

```sh
./bin/walky daemon            # foreground; polls the mount table
./bin/walky status            # asks the running daemon over D-Bus
./bin/walky sync --wait       # force a sync now
./bin/walky eject             # flush, unmount and power off the drive
```

The daemon claims `io.github.ijin.Walky1` on the session bus, watches the mount
table for a store whose `walky.json` matches the configured `repo_id`, and
starts a sync on attach. While the drive stays attached it re-scans the local
folder every `[sync] poll_seconds` (180 by default, 0 disables), so new files are
picked up without pressing Sync. This is polling, not inotify; a scan with no
changes does not write to the drive. If a filesystem labelled `WALKY` is present
but not mounted, it asks udisks2 to mount it (disable with `auto_mount = false`). `--device <dir>` and `--repo <dir>` allow manual test
runs. Without a running daemon, `walky status` falls back to local information.

`make install-local` also installs a user unit with `%h/.local/bin/walky`;
`sudo make install install-data` installs the system-wide one. Enable it with
`systemctl --user enable --now walky.service` (not enabled automatically).

## Installation

```sh
./install.sh              # interactive menu
./install.sh local        # build and install for the current user (~/.local)
./install.sh system       # system-wide, requires root (/usr)
./install.sh uninstall    # remove the user installation
./install.sh uninstall-system
```

Running `./install.sh` without arguments opens a menu:

1. install for the current user,
2. install system-wide,
3. stop the service and uninstall the user copy,
4. uninstall the system copy,
5. service status (enable/disable the user service).

`./install.sh local` prints the unit path, restarts `walky.service` if it is
already active, and offers to enable and start it. `./install.sh service` shows
the status and offers the matching action (enable, start, restart, stop or
disable); `enable`, `disable`, `start`, `stop` and `restart` do the same
non-interactively. Uninstalling stops and disables the service before removing
the unit.

The script builds both binaries and installs `walky`, `walky-ui`, the desktop
entry and a systemd user unit. The local mode needs no root and rewrites the unit
to use `%h/.local/bin/walky`; it does not enable the service. After a system
install:

```sh
systemctl --user daemon-reload
systemctl --user enable --now walky.service
```

`walky.service` is a user unit with D-Bus activation; the system-wide udev mode
is in `data/99-walky-usb.rules` (commented out). Scheduled maintenance is
configured in `[maintenance]` (`auto_gc`, `gc_interval_hours`) and
`[retention] machine_days`. Distribution packaging is
deferred; the installer and README are the supported installation path for now.

## Platform support

Walky runs on Linux. The drive is ext4, which Windows and macOS cannot mount out
of the box, so "plug into any computer and browse files" is explicitly not
supported. Reading on another machine means a Linux live environment (or WSL2
with `wsl --mount`) with Walky installed:

```sh
walky restore --store /mnt/WALKY --dest ~/Restored
```

Supporting Windows/macOS would require giving up ext4 guarantees, which was
considered and rejected.

LUKS2 is **strongly recommended, though optional and never enforced**. The drive
is portable and easy to lose: without encryption, anyone who finds or steals it
can read every file and the full history. Walky does not require LUKS and will
not refuse a plain ext4 drive; it only checks the filesystem. LUKS is a
container, not a filesystem — after unlocking, the filesystem inside is still
ext4, so an encrypted drive needs no special configuration. Create the container
with GNOME Disks or `cryptsetup`.

## Data safety

The drive is not a backup. Until a second replica exists, losing the drive means
losing the data. The filesystem must be ext4. LUKS2 is optional and not
enforced, but **strongly recommended**: a lost or stolen unencrypted drive
exposes all data and history. Walky deliberately refuses only non-ext4
filesystems, never unencrypted ones.

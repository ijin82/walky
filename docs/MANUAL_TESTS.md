# Manual test checklist

Automated tests are the default (`AGENTS.md` §4.1). This file covers only what
cannot be tested automatically: real drives, LUKS prompts, a real GNOME
session, suspend, real USB throughput and power loss. Run the affected items
for every change; run the whole list before a release or milestone sign-off.

Rules:

- If a check can be automated, automate it and remove it from this file.
- A failed check needs a fix and, when possible, an automated regression test.
- Record runs in the log at the bottom and keep `PLAN.md` in sync.
- Prefer a disposable drive for destructive checks (corruption, no space,
  unplugging, power loss).

## Preparation

- USB 3.0 flash drive formatted ext4 with a label (default `WALKY`); one drive
  inside LUKS2 and, ideally, one without.
- A representative tree: about 15 GB, several git repositories, symlinks,
  executable files and many small files.
- A second machine or a clean user account for restore and conflict checks.
- Fresh build: `make build`, `./install.sh local`; note the version.
- A terminal and the desktop session running the same user.

## Storage and recovery

### MT-01 Initialize from the CLI

1. Mount the drive and run `./bin/walky init /run/media/$USER/<label>`.
2. Run the same command again.

Expected: the first run prints `repo_id`, creates `/WALKY` and `walky.json`,
and touches nothing outside the store root. The second run fails with
`already initialized`. Record the `repo_id`.

### MT-02 First snapshot and verify

1. `time ./bin/walky snapshot --store … --repo ~/Sync`.
2. `./bin/walky verify --store … --full`.

Expected: progress shows scanned/stored counts; `verify` prints `ok`. Record
elapsed time, RSS and USB speed in `PLAN.md`.

### MT-03 Increment and index

1. Edit ~100 files, including one large file and one git commit.
2. Run the snapshot again and read the cached/hashed counters.

Expected: only changed files are hashed, the run takes seconds, and the store
grows by roughly the size of the changes.

### MT-04 Restore on a clean machine

1. Attach the drive to a second machine and run `verify --full`.
2. `restore --dest ~/Restored`.
3. `diff -r`, compare `stat` for several files, check symlink targets and modes.
4. In a restored git repository run `git fsck --no-progress`.

Expected: byte-identical files, same modes and mtimes, correct symlinks, no
writes outside the destination, `git fsck` clean.

### MT-05 Unplug during a snapshot

1. Start a snapshot of the 15 GB tree.
2. Unplug the drive while objects are being written, then replug it.

Expected: the command fails with an I/O error, `HEAD` still points at the
previous generation, `verify` passes, and the next snapshot completes. No
half-written generation becomes visible.

### MT-06 Corrupt data on the drive

1. Flip one byte in a `.pack` file (or truncate an object/pack).
2. Run `verify --full` and `restore`.

Expected: `verify` names the corrupt path and exits non-zero; `restore` refuses
instead of writing broken data.

### MT-07 No free space

1. Fill the drive so less than the pending snapshot needs remains.
2. Snapshot new large files.

Expected: a clear "no space" error, `HEAD` unchanged, `verify` still passes, the
drive stays mountable and usable after freeing space.

### MT-08 USB throughput

1. Measure a first snapshot and a 100-file increment on USB 3.0 (and USB 2.0 if
   available).

Expected: increments in seconds; first upload within the estimates in
`PLAN.md` (2–5 min USB 3.0, 10–17 min USB 2.0). Record both numbers.

### MT-09 Retention and GC

1. Set `[retention] daily = 1`, `weekly = 0`, `monthly = 0`, take two snapshots
   on the same day, then run `walky gc --dry-run` and `walky gc`.
2. Repeat with a copy of the drive and `daily = 2` to check that the newest
   snapshot of each bucket survives.
3. `verify --full`; restore an old generation and a file from it.

Expected: dry-run reports without deleting; GC keeps HEAD, the requested buckets,
machine bases and non-expired trash; old generations and their unreferenced
objects disappear; restore of a kept generation works.

### MT-10 Trash expiry

1. Delete files locally, sync, and run `walky trash list --store …`.
2. Restore a deleted file via `walky restore`.
3. Empty/expire trash (`trash empty`, `gc --trash-days 0`) on a copy of the
   drive.

Expected: deleted files are listed and recoverable for `trash_days`; after
expiry GC can free their objects; nothing outside the trash is deleted early.

## Setup and configuration

### MT-11 Initialize a drive from the GUI

1. Plug a mounted ext4 drive (LUKS unlocked) and open Walky.
2. Choose "Set up a drive…", select the drive and a local folder, press
   "Initialize drive".

Expected: `/WALKY` appears, the config gets `repo.path` and `repo_id`, the main
window shows the drive, and the first sync starts without a terminal. Existing
files on the drive are untouched.

### MT-12 Adopt an existing store in the GUI

1. Reopen the wizard for a drive that already has `/WALKY`.
2. Select "Use this drive" and a local folder.

Expected: the store is marked "store ready"; the config points at it; no store
is recreated and no data is changed.

### MT-13 Initialize and configure in one CLI step

1. `./bin/walky init /run/media/$USER/<label> --repo ~/Sync`.
2. Start the daemon.

Expected: `repo.path` and `repo_id` are written; the daemon attaches the drive
and syncs; no manual config edit is needed.

### MT-14 LUKS and unencrypted drives

1. Run `init` on an ext4 drive that is **not** inside LUKS.
2. Run `init` on a LUKS2+ext4 drive after unlocking it in GNOME.

Expected: the unencrypted drive prints the advisory LUKS warning and still
initializes; the LUKS drive prints no warning. Walky never asks for or stores a
LUKS passphrase.

## Drive lifecycle and daemon

### MT-15 Plug detection and automount

1. Label the filesystem `WALKY` (or set `[device] label` to its label) and
   unmount it.
2. Plug it in with the daemon running; let the desktop mount it, then repeat
   with `auto_mount = true` while nothing mounts it.

Expected: `walky status` shows the device within seconds, the journal logs the
attach, and no root terminal is needed (a polkit prompt is acceptable).

### MT-16 Autosync on attach

Unplug, change files locally, plug the drive back.

Expected: sync starts automatically, a notification appears, `HEAD` advances
once, and the recent-files list updates.

### MT-17 Safe eject

Press "Eject" (UI, tray or `walky eject`) while a sync is running.

Expected: the commit finishes, the drive is unmounted and powered off, and a
"safe to remove" notification appears.

### MT-18 Suspend during a commit

Suspend the machine during a large snapshot.

Expected: writes stop before suspend; after resume the snapshot completes or
aborts cleanly; `verify` passes.

### MT-19 Daemon restart mid-sync

`systemctl --user restart walky.service` during a snapshot.

Expected: the next start recovers; no data loss; `HEAD` consistent; the sync can
be started again.

### MT-20 Daemon missing and Start

1. Stop the daemon (`./install.sh stop`).
2. Open the UI.

Expected: a friendly "Walky daemon is not running" banner with a **Start**
button; pressing it starts the service and the normal status appears. The raw
GDBus error is never shown.

### MT-21 Service enable at login

1. `./install.sh service` and enable the unit.
2. Log out and back in.

Expected: the daemon is active without opening the UI; the tray icon appears;
`systemctl --user is-enabled walky.service` says `enabled`.

### MT-22 Tray Quit and window Quit

1. Choose "Quit Walky" from the tray menu.
2. Restart the UI and close the window with the window's Quit action.

Expected: tray Quit stops the daemon **and** closes the window. Window Quit
closes only the window; the daemon and tray stay alive.

### MT-36 Automatic pickup of local changes

1. Attach the drive and wait until the window shows `idle`.
2. Create a folder with a subfolder and a file, or edit an existing file.
3. Do not press Sync; wait for `poll_seconds` (set it to 5 in `config.toml` for
   the test, then restore 30).

Expected: a sync starts on its own within the interval, the file appears in the
history, and the recent-files list shows it. A poll with no changes must not
advance the generation or rewrite the store.

## Two-way sync

### MT-23 Conflict between two machines

Change the same file on two machines while the drive is away, then sync both.

Expected: both variants survive; the conflict copy is named
`name.conflict-<host>-<time>`; the UI and log report it.

### MT-24 Deletion does not resurrect

Delete a file on A, sync, then attach B.

Expected: the file is deleted on B and kept in the trash, not restored from B's
stale copy.

### MT-25 Offline machine returns

Keep machine B offline while A changes and deletes many files, then attach B.

Expected: a three-way merge against B's base; no unexpected deletions or
resurrections; conflicts only for genuinely divergent files.

## Desktop UI and tray

### MT-26 Window and status

Open the app during a sync.

Expected: state, device, generation, free space, progress and the last five
files update; buttons work; a crashed `walky-ui` never stops the daemon.

### MT-27 Tray icon and menu

Check the top bar in light and dark themes.

Expected: a monochrome symbolic Walky icon (correctly recolored), menu items
Sync now, Pause/Resume, Open Walky, Verify integrity, Quit Walky; clicking the
icon opens the window.

### MT-28 Notifications

Finish a sync and trigger an error.

Expected: exactly one notification per event, no duplicates, no notification
storm during repeated syncs.

### MT-29 Settings apply and persist

1. In Settings, add `**/vendor` or a project-specific exclude, change retention
   and the conflict policy, save.
2. Run a sync, restart the daemon, reopen Settings.

Expected: excludes take effect on the next scan; retention is used by the next
GC; the conflict policy applies to new conflicts; values persist across the
daemon restart (comments in the TOML are lost, known).

## Installer and packaging

### MT-30 Local install surface

1. Run `./install.sh local` from a clean user account.
2. Check the app grid and the tray.

Expected: exactly one Walky launcher (no duplicate `walky.desktop`), icon corners
transparent at 256 px, symbolic tray icon, `~/.local/bin/walky` and `walky-ui`,
and the unit in `~/.config/systemd/user`.

### MT-31 Installer service management

1. Run `./install.sh service`; try enable, start, stop and disable.
2. Run `./install.sh start|stop|enable|disable`.
3. Run `./install.sh local` while the service is active but disabled.

Expected: status always shows Unit/Enabled/Active/Binaries; prompts accept
Enter as yes; a running-but-disabled service can be enabled without stopping;
the resulting state is printed.

### MT-32 Uninstall and reinstall

1. `./install.sh uninstall` with the service running.
2. Reinstall and check the app grid.

Expected: uninstall stops and disables the service, removes binaries, desktop
entry, icons and unit, but keeps `~/.config/walky`, the repo folder, local
trash and machine state. Reinstall produces exactly one launcher and no stale
`walky.desktop`.

## Durability and power loss

### MT-33 Hard power loss during a commit

1. Start a snapshot and cut power (or yank the drive) at several moments.
2. Reboot, attach the drive and run `verify --full`.

Expected: `HEAD` is either the old or the fully committed generation; `verify`
passes; `walky gc` removes garbage; no manual repair is needed.

### MT-35 Lost drive recovery

1. Two machines, A and B, both synced once. Make a change on A and a different
   change on B; leave B offline. Make a deletion on A and a deletion on B.
2. Simulate the loss: keep the old drive away (or use a copy and discard it).
3. Back up both folders, `~/.local/state/walky` and `config.toml`.
4. On A, initialize a new drive, then use "Recover drive…" in the window.
5. On B, attach the new drive, choose "Use this drive" with B's existing folder,
   then "Recover drive…".

Expected: both machines show **Recovery required** instead of syncing silently.
The preview reports upload/download/conflict counts and **zero deletions**.
After the merge, A's current files and B's unsynced files are present on both
machines; differing same-name files keep both variants as conflict copies; the
config points at the new `repo_id`; a second sync runs normally. Files deleted
before the loss may reappear — that is the documented additive behaviour. Old
generations are absent.

### MT-34 Attach/detach loop

1. Plug and unplug the drive ten times, sometimes during a sync.
2. Watch the daemon for stuck locks, duplicate syncs or leaks.

Expected: each attach syncs once; detach cancels cleanly; no stale `flock`, no
zombie sync, memory returns near baseline.

## Run log

| Date | Version | Checks run | Result | Notes |
|---|---|---|---|---|
| | | | | |

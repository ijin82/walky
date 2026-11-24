# Manual test checklist

Automated tests are the default (see `AGENTS.md` §4.1). This file covers only
what cannot be tested automatically: real hardware, a real desktop session,
real power loss. Everything here is executed by a human before a release or a
milestone sign-off.

Rules:

- Run only the checks affected by the change; run everything before a release.
- If a check can be automated, automate it and delete the item from this file.
- When a check fails, fix the bug and add an automated regression test if the
  failure can be reproduced without special hardware.
- Record the run in the log at the bottom.

## Preparation

- A real USB 3.0 flash drive, formatted ext4, label `WALKY` (LUKS once M8 lands).
- A representative tree: about 15 GB, several git repositories, symlinks,
  executable files, many small files.
- A second machine or a clean user account for restore checks.
- `./bin/walky` from a fresh `make build`; note the version.

## Storage (M1–M2)

### MT-01 Initialize a real drive

1. `./bin/walky init /run/media/$USER/WALKY`
2. Run it a second time.

Expected: the first run prints `repo_id` and exits 0, `/WALKY/` contains the
layout and `walky.json`. The second run fails with `already initialized`. No
files outside `/WALKY/` are touched.

### MT-02 First snapshot of 15 GB

1. `time ./bin/walky snapshot --store /run/media/$USER/WALKY --repo ~/Sync`
2. `./bin/walky verify --store /run/media/$USER/WALKY --full`

Expected: the progress output reports scanned/stored counts; `verify` prints
`ok`. Record the elapsed time and RSS in `PLAN.md`.

### MT-03 Increment and index

1. Edit ~100 files in the tree.
2. Run the snapshot again and read the `cached/hashed` counters.

Expected: only the changed files are hashed; the snapshot takes seconds; the
store grows by roughly the size of the changes.

### MT-04 Restore on another machine

1. Copy nothing; attach the drive to a second machine.
2. `./bin/walky verify --store … --full`
3. `./bin/walky restore --store … --dest ~/Restored`
4. `diff -r ~/Sync ~/Restored` and compare `stat` output for several files.

Expected: byte-identical content, same modes, symlink targets, mtimes; no
writes outside the destination.

### MT-05 Unplug during a snapshot

1. Start a snapshot of a large tree.
2. Unplug the drive while objects are being written; replug it.

Expected: the command fails with an I/O error, `HEAD` keeps the previous
generation, `verify` on the previous generation passes, the next snapshot
completes. No half-written snapshot becomes visible.

### MT-06 Corrupt data on the drive

1. Flip one byte inside a `.pack` file (or truncate an object).
2. `./bin/walky verify --store … --full` and `./bin/walky restore …`.

Expected: `verify` reports the corrupt path and exits non-zero; `restore`
refuses instead of writing broken data.

### MT-07 No free space

1. Fill the drive so that less than the pending snapshot needs remains.
2. Run a snapshot with new large files.

Expected: a clear "no space" error, `HEAD` unchanged, `verify` still passes, the
drive remains mountable.

### MT-08 Real USB throughput

1. Measure a first snapshot and a 100-file increment on USB 3.0 and, if
   available, USB 2.0.
2. Record both numbers in `PLAN.md`.

Expected: increments in seconds; the first upload within the estimates in
`PLAN.md` (2–5 min USB 3.0, 10–17 min USB 2.0).

## Daemon (M3)

Automated tests cover the state machine, marker validation and cancellation.
The checks below require a real drive and a real session bus.

### MT-10 Plug detection

Plug the drive with the daemon running. Expected: `walky status` shows the
device within a second; the journal logs the attach; no root prompts.

### MT-11 Autosync on attach

Unplug, change files locally, plug the drive back. Expected: sync starts
automatically, a notification appears, `HEAD` advances once.

### MT-12 Safe eject

Press "Eject" while a sync is running. Expected: the commit finishes, the drive
is unmounted and powered off, a "safe to remove" notification appears.

### MT-13 Suspend during a commit

Suspend the machine while a large snapshot runs. Expected: writes stop and sync
before suspend; after resume the snapshot either completes or aborts cleanly.

### MT-14 Daemon restart mid-sync

`systemctl --user restart walky` during a snapshot. Expected: the next start
recovers, no data loss, `HEAD` consistent.

## Two-way sync (M4)

Automated tests cover convergence, deletions, conflicts and trash. The checks
below exercise two real machines and a real drive.

### MT-15 Conflict between two machines

Change the same file on two machines while the drive is away. Expected: both
variants survive, the conflict copy is named
`name.conflict-<host>-<time>`, the UI/log reports it.

### MT-16 Deletion does not resurrect

Delete a file on machine A, sync, then attach machine B. Expected: the file is
deleted on B and kept in the trash, not restored from B's stale copy.

## UI (M6) — pending implementation

### MT-20 Window and status

Open the app. Expected: status, free space, last 5 files and progress are
visible and update during a sync.

### MT-21 Visible as running

Close the window, check GNOME Quick Settings / the tray. Expected: the app is
listed as running in the background and can be reopened.

### MT-22 Notifications

Finish a sync and trigger an error. Expected: exactly one notification per
event, no duplicates.

## Encryption (M8) — pending implementation

### MT-23 LUKS unlock

Plug the drive after a reboot. Expected: a passphrase/keyfile prompt, no
plaintext access without it, the marker is unreadable before unlock.

## Run log

| Date | Version | Checks run | Result | Notes |
|---|---|---|---|---|
| | | | | |

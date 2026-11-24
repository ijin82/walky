# Walky — an offline "cloud" on a USB drive

Two-way synchronization of a folder with a USB flash drive that acts as the
remote server of Dropbox/Yandex Disk: version history, deduplication, conflict
handling and restore onto a new machine. No internet is required — the transport
is physical.

Status: **M0–M4**. Storage, daemon and two-way sync work: `init`, `snapshot`,
`ls`, `head`, `restore`, `verify`, `daemon`, `status`, `sync`, `eject` — blake3
CAS, generations, atomic commits, restore onto a clean machine, a hash index,
packs, drive detection and bidirectional sync with conflict handling. The UI is
still a skeleton.
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
data/              systemd unit, udev, sysctl, .desktop, example config
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
make install-local  # no root: ~/.local/bin + .desktop
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

## Quick start (M1–M2, no daemon)

```sh
# the drive is already formatted as ext4 and mounted
./bin/walky init /run/media/$USER/WALKY
./bin/walky snapshot --store /run/media/$USER/WALKY --repo ~/Sync
./bin/walky ls --store /run/media/$USER/WALKY --long
./bin/walky verify --store /run/media/$USER/WALKY --full

# restore on another machine
./bin/walky restore --store /run/media/$USER/WALKY --dest ~/Restored
```

`--store` is the drive mount point, `--repo` is the synchronized folder. `init`
does not format the drive: mkfs/LUKS will be a separate command (`walky format`)
so a disk cannot be wiped by accident.

Current limitations: no GC or retention yet (M5), no UI (M6). The daemon only uses drives the desktop has
already mounted; auto-mounting through udisks2 without a polkit prompt is a
follow-up. Formatting and LUKS are a separate command in M8.

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

## Daemon (M3)

```sh
./bin/walky daemon            # foreground; polls the mount table
./bin/walky status            # asks the running daemon over D-Bus
./bin/walky sync --wait       # force a sync now
./bin/walky eject             # flush, unmount and power off the drive
```

The daemon claims `io.github.ijin.Walky1` on the session bus, watches the mount
table for a store whose `walky.json` matches the configured `repo_id`, and
starts a sync on attach. `--device <dir>` and `--repo <dir>` allow manual test
runs. Without a running daemon, `walky status` falls back to local information.

`make install-local` also installs a user unit with `%h/.local/bin/walky`;
`sudo make install install-data` installs the system-wide one. Enable it with
`systemctl --user enable --now walky.service` (not enabled automatically).

## Installation (after M3)

```sh
sudo make install install-data
systemctl --user daemon-reload
systemctl --user enable --now walky.service
```

`walky.service` is a user unit with D-Bus activation; the system-wide udev mode
is in `data/99-walky-usb.rules` (commented out).

## Data safety

The drive is not a backup. Until a second replica exists, losing the drive means
losing the data. The target filesystem is ext4 + LUKS2 (encryption lands in M8).

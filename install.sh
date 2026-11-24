#!/usr/bin/env bash
# Walky installer.
#
# Usage:
#   ./install.sh                   build and install for the current user
#   ./install.sh local             same as above (~/.local, no root)
#   ./install.sh system            install under /usr (requires root)
#   ./install.sh uninstall         remove the current user installation
#   ./install.sh uninstall-system  remove the system installation (root)
#
# Walky never formats a drive. Any mounted filesystem works; ext4 is
# recommended because it preserves modes, symlinks and hardlinks.

set -euo pipefail
cd "$(dirname "$0")"

if ! command -v make >/dev/null 2>&1; then
    echo "error: make is required" >&2
    exit 1
fi

case "${1:-local}" in
    local)
        make install-local
        ;;
    system)
        if [ "$(id -u)" -ne 0 ]; then
            echo "error: system install requires root: sudo ./install.sh system" >&2
            exit 1
        fi
        make install install-data
        ;;
    uninstall)
        make uninstall-local
        ;;
    uninstall-system)
        if [ "$(id -u)" -ne 0 ]; then
            echo "error: uninstalling the system copy requires root" >&2
            exit 1
        fi
        make uninstall
        ;;
    -h|--help)
        sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'
        ;;
    *)
        echo "usage: $0 [local|system|uninstall|uninstall-system]" >&2
        exit 2
        ;;
esac

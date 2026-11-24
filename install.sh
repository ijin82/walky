#!/usr/bin/env bash
# Walky installer.
#
#   ./install.sh                 interactive menu
#   ./install.sh local           install for the current user (~/.local)
#   ./install.sh system          install system-wide (/usr, requires root)
#   ./install.sh uninstall       remove the current-user installation
#   ./install.sh uninstall-system remove the system installation (root)
#
# Walky never formats a drive. Any mounted ext4 filesystem works; LUKS is
# optional but strongly recommended.

set -euo pipefail
cd "$(dirname "$0")"

if ! command -v make >/dev/null 2>&1; then
    echo "error: make is required" >&2
    exit 1
fi

info() { echo "==> $*"; }
warn() { echo "!!! $*" >&2; }

usage() {
    cat <<'USAGE'
Usage: ./install.sh [command]

Without a command an interactive menu is shown.

Commands:
  local             Build and install for the current user (~/.local, no root)
  system            Build and install system-wide under /usr (requires root)
  uninstall         Remove the current-user installation
  uninstall-system  Remove the system installation (requires root)
  help              Show this help

Examples:
  ./install.sh
  ./install.sh local
  sudo ./install.sh system
USAGE
}

install_local() {
    info "installing for ${USER} into ~/.local"
    make install-local
    echo
    echo "Done. Start the daemon and the window with:"
    echo "  systemctl --user daemon-reload"
    echo "  systemctl --user enable --now walky.service"
    echo "  walky-ui"
}

install_system() {
    if [ "$(id -u)" -ne 0 ]; then
        warn "system install requires root"
        echo "  run: sudo ./install.sh system" >&2
        exit 1
    fi
    info "installing system-wide under /usr"
    make install install-data
    echo
    echo "Done. Enable the user service for each user with:"
    echo "  systemctl --user daemon-reload"
    echo "  systemctl --user enable --now walky.service"
}

uninstall_local() {
    info "removing the current-user installation"
    make uninstall-local
    echo "Done."
}

uninstall_system() {
    if [ "$(id -u)" -ne 0 ]; then
        warn "removing the system installation requires root"
        echo "  run: sudo ./install.sh uninstall-system" >&2
        exit 1
    fi
    info "removing the system installation"
    make uninstall
    echo "Done."
}

confirm_uninstall() {
    local answer
    echo
    echo "  This removes the binaries, desktop entry, icons and the user unit"
    echo "  for the current user. Your data and configuration are kept."
    printf "  Continue? [y/N]: "
    if ! read -r answer; then
        echo
        return 1
    fi
    case "${answer}" in
        y|Y|yes|YES) return 0 ;;
        *) echo "  Cancelled."; return 1 ;;
    esac
}

interactive_menu() {
    local choice
    if [ ! -t 0 ]; then
        install_local
        return 0
    fi

    cat <<'MENU'

  Walky installer

    1) Install for me        build and install into ~/.local (no root)
    2) Install system-wide   build and install under /usr (requires root)
    3) Uninstall             remove the current-user installation
    4) Uninstall system      remove the system installation (root)
    q) Quit

MENU

    while true; do
        printf "  Choose [1/2/3/4/q]: "
        if ! read -r choice; then
            echo
            return 0
        fi
        case "${choice}" in
            1|local)
                install_local
                return 0
                ;;
            2|system)
                install_system
                return 0
                ;;
            3|uninstall)
                if confirm_uninstall; then
                    uninstall_local
                fi
                return 0
                ;;
            4|uninstall-system)
                uninstall_system
                return 0
                ;;
            q|Q|quit|exit)
                echo "  Nothing to do."
                return 0
                ;;
            "") ;;
            *)
                echo "  Please choose 1, 2, 3, 4 or q."
                ;;
        esac
    done
}

case "${1:-menu}" in
    menu) interactive_menu ;;
    local) install_local ;;
    system) install_system ;;
    uninstall) uninstall_local ;;
    uninstall-system) uninstall_system ;;
    -h|--help|help) usage ;;
    *)
        echo "Unknown command: $1" >&2
        usage >&2
        exit 1
        ;;
esac

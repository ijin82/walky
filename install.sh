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

USER_UNIT="${XDG_CONFIG_HOME:-${HOME}/.config}/systemd/user/walky.service"
SYSTEM_UNIT="/usr/lib/systemd/user/walky.service"

systemd_available() {
    command -v systemctl >/dev/null 2>&1 && [ -n "${XDG_RUNTIME_DIR:-}" ]
}

service_enabled() {
    systemctl --user is-enabled walky.service 2>/dev/null || true
}

service_active() {
    systemctl --user is-active walky.service 2>/dev/null || true
}

service_status() {
    local enabled active unit
    enabled="$(service_enabled)"
    active="$(service_active)"
    if [ -f "${USER_UNIT}" ]; then
        unit="${USER_UNIT}"
    elif [ -f "${SYSTEM_UNIT}" ]; then
        unit="${SYSTEM_UNIT} (system)"
    else
        unit="not installed"
    fi
    echo "  Unit:    ${unit}"
    echo "  Enabled: ${enabled:-unknown}"
    echo "  Active:  ${active:-unknown}"
    echo "  Binaries: $(command -v walky 2>/dev/null || echo "not in PATH")"
    if [ "${active}" = "active" ] && [ "${enabled}" != "enabled" ]; then
        echo "  Note:    running now, but it will not start at login."
    fi
}

enable_service() {
    if ! systemd_available; then
        warn "systemd user session is not available; start it manually: walky daemon"
        return 1
    fi
    systemctl --user enable --now walky.service
    info "walky.service enabled and started"
    service_status
}

disable_service() {
    if ! systemd_available; then
        return 0
    fi
    systemctl --user disable --now walky.service 2>/dev/null || true
    info "walky.service stopped and disabled"
    service_status
}

restart_service() {
    if ! systemd_available; then
        warn "systemd user session is not available; restart 'walky daemon' manually"
        return 1
    fi
    systemctl --user daemon-reload
    if systemctl --user restart walky.service; then
        info "walky.service restarted"
        service_status
    else
        warn "failed to restart; see: journalctl --user -u walky.service -n 30"
        return 1
    fi
}

offer_service() {
    local answer
    if ! systemd_available; then
        echo "  systemd is not available here; run 'walky daemon' manually."
        return 0
    fi
    local enabled active
    enabled="$(service_enabled)"
    active="$(service_active)"
    if [ "${active}" = "active" ] && [ "${enabled}" = "enabled" ]; then
        echo "  The background service is already enabled and running."
        return 0
    fi
    if [ "${active}" = "active" ]; then
        printf "  Enable it so it starts at login too? [Y/n]: "
    else
        printf "  Enable and start Walky in the background now? [Y/n]: "
    fi
    if ! read -r answer; then
        echo
        echo "  Later: systemctl --user enable --now walky.service"
        return 0
    fi
    case "${answer}" in
        n|N|no|NO)
            echo "  Skipped. Later: systemctl --user enable --now walky.service"
            ;;
        *)
            enable_service
            ;;
    esac
}

manage_service() {
    local answer
    echo
    echo "  Walky background service"
    service_status
    echo
    if [ ! -f "${USER_UNIT}" ] && [ ! -f "${SYSTEM_UNIT}" ]; then
        echo "  Install first, then the service can be enabled."
        return 0
    fi
    if ! systemd_available; then
        return 0
    fi
    local enabled active
    enabled="$(service_enabled)"
    active="$(service_active)"
    if [ "${enabled}" = "enabled" ]; then
        if [ "${active}" = "active" ]; then
            echo "  It is enabled and running."
            printf "  Action: [r]estart, [d]isable and stop, or [n]othing [r/d/N]: "
            if read -r answer; then
                case "${answer}" in
                    r|R|restart) restart_service ;;
                    d|D|disable|y|Y|yes|YES) disable_service ;;
                    *) echo "  Left running and enabled." ;;
                esac
            fi
        else
            echo "  It is enabled but not running."
            echo "  Press Enter (or type y) to start it now, or run:"
            echo "    systemctl --user start walky.service"
            printf "  Start it now? [Y/n]: "
            if read -r answer; then
                case "${answer}" in
                    n|N|no|NO) echo "  Left stopped. Start later: systemctl --user start walky.service" ;;
                    *)
                        if systemctl --user start walky.service; then
                            info "walky.service started"
                            service_status
                        else
                            warn "failed to start; see: journalctl --user -u walky.service -n 30"
                        fi
                        ;;
                esac
            fi
        fi
    elif [ "${active}" = "active" ]; then
        echo "  It is running but will not start at login."
        echo "  Press Enter to enable autostart, or n to leave it as is."
        printf "  Enable it? [Y/n]: "
        if read -r answer; then
            case "${answer}" in
                n|N|no|NO) echo "  Left running without autostart." ;;
                *) enable_service ;;
            esac
        fi
    else
        echo "  It is disabled and stopped."
        echo "  Press Enter (or type y) to enable and start it now, or run:"
        echo "    systemctl --user enable --now walky.service"
        printf "  Enable and start it? [Y/n]: "
        if read -r answer; then
            case "${answer}" in
                n|N|no|NO) echo "  Left disabled. Later: systemctl --user enable --now walky.service" ;;
                *)
                    if enable_service; then
                        :
                    else
                        warn "failed to enable; see: journalctl --user -u walky.service -n 30"
                    fi
                    ;;
            esac
        fi
    fi
}

usage() {
    cat <<'USAGE'
Usage: ./install.sh [command]

Without a command an interactive menu is shown.

Commands:
  local             Build and install for the current user (~/.local, no root)
  system            Build and install system-wide under /usr (requires root)
  uninstall         Remove the current-user installation
  uninstall-system  Remove the system installation (requires root)
  service           Show service status, enable or disable it
  enable            Enable and start the user service
  disable           Stop and disable the user service
  start             Start the user service
  stop              Stop the user service
  restart           Reload unit and restart the user service
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
    if systemd_available; then
        systemctl --user daemon-reload
        if [ "$(service_active)" = "active" ]; then
            systemctl --user restart walky.service && info "walky.service restarted"
        fi
    fi
    echo
    echo "Done."
    service_status
    offer_service
    echo
    echo "Start the window with: walky-ui"
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
    echo "Done."
    echo "  The unit is in ${SYSTEM_UNIT}."
    echo "  Each user enables it for their own session:"
    echo "    systemctl --user daemon-reload"
    echo "    systemctl --user enable --now walky.service"
}

uninstall_local() {
    info "removing the current-user installation"
    disable_service
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
    3) Uninstall             stop the service and remove the current-user install
    4) Uninstall system      remove the system installation (root)
    5) Service               show status, enable/disable the user service
    q) Quit

MENU

    while true; do
        printf "  Choose [1/2/3/4/5/q]: "
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
            5|service)
                manage_service
                return 0
                ;;
            q|Q|quit|exit)
                echo "  Nothing to do."
                return 0
                ;;
            "") ;;
            *)
                echo "  Please choose 1, 2, 3, 4, 5 or q."
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
    service) manage_service ;;
    enable) enable_service ;;
    disable) disable_service ;;
    start)
        systemctl --user start walky.service && info "walky.service started"
        service_status
        ;;
    stop)
        systemctl --user stop walky.service && info "walky.service stopped"
        service_status
        ;;
    restart) restart_service ;;
    -h|--help|help) usage ;;
    *)
        echo "Unknown command: $1" >&2
        usage >&2
        exit 1
        ;;
esac

#!/bin/sh
# Uninstall quai-node.
#
#   curl -fsSL https://raw.githubusercontent.com/mpoletiek/quai-node/main/uninstall.sh | sh
#
# It runs `quai-node uninstall`, which stops and removes the quai-node
# service and removes go-quai, quai-dash and quai-node. The chain data, the
# node's key, its logs and settings stay, so a later install picks up where
# it left off. To delete those too:
#
#   curl -fsSL https://raw.githubusercontent.com/mpoletiek/quai-node/main/uninstall.sh | sh -s -- --purge
#
# Arguments after `sh -s --` go to `quai-node uninstall` (--purge, --yes,
# --dry-run).
#
# If quai-node's home was already deleted by hand, quai-node went with it.
# Then this removes what it left outside that home: the service, and the
# quai-node and quai-dash links in ~/.local/bin. It removes only those that
# point into a home that no longer exists.

set -eu

BIN="quai-node"
LABEL_PREFIX="io.github.mpoletiek.quai-node."

say() { printf '%s\n' "$*"; }
fail() { printf 'uninstall: %s\n' "$*" >&2; exit 1; }

# Read in full before anything runs, so a download cut off halfway can't
# run half of it.
main() {
    [ "$(id -u)" != 0 ] || fail "run this as your own user, not root: quai-node asks for sudo itself to remove the service"
    binary="$HOME/.local/bin/$BIN"
    if [ ! -x "$binary" ]; then
        binary="$(command -v "$BIN" 2>/dev/null || true)"
        if [ -z "$binary" ]; then
            leftovers "$@"
            return
        fi
    fi
    # Only ever run a file that says it is quai-node.
    version="$("$binary" version 2>/dev/null | head -n 1 || true)"
    case "$version" in
        "$BIN "*) ;;
        *) fail "$binary does not identify itself as quai-node; leaving it alone" ;;
    esac

    say "Uninstalling $version"
    if [ -r /dev/tty ] && (: </dev/tty) 2>/dev/null; then
        "$binary" uninstall "$@" </dev/tty
    else
        "$binary" uninstall "$@"
    fi
}

# leftovers removes what a deleted quai-node home left behind.
leftovers() {
    dry=0 yes=0
    for a in "$@"; do
        case "$a" in
            --dry-run) dry=1 ;;
            --yes | -y) yes=1 ;;
            --purge) ;;
            *) fail "unknown option $a" ;;
        esac
    done
    found="$(scan list)"
    [ -n "$found" ] || fail "no $BIN in ~/.local/bin or on your PATH, and nothing of it left behind; nothing to uninstall"

    say "quai-node is gone (its home was deleted), but it left these behind:"
    printf '%s\n' "$found" | sed 's/^/  /'
    say "Chain data kept outside that home, if any, stays."
    [ "$dry" = 0 ] || return 0
    if [ "$yes" = 0 ]; then
        if [ -r /dev/tty ] && (: </dev/tty) 2>/dev/null; then
            printf 'Remove them? [y/N] ' >/dev/tty
            read -r ans </dev/tty || ans=""
            case "$ans" in
                y | Y | yes | YES) ;;
                *) fail "nothing removed" ;;
            esac
        else
            fail "nothing removed; run it with '| sh -s -- --yes' to remove them"
        fi
    fi
    scan remove
    say "Removed."
}

# scan lists (or removes, with "remove") each leftover: a link quai-node
# made whose target is gone, and a service it installed whose program is.
scan() {
    for name in quai-node quai-dash; do
        if dangling "$HOME/.local/bin/$name" "*/$name"; then
            act "$1" link "$HOME/.local/bin/$name"
        fi
    done
    dash_conf="${XDG_CONFIG_HOME:-$HOME/.config}/quai-dash/config.toml"
    if dangling "$dash_conf" "*/dash/config.toml"; then
        act "$1" link "$dash_conf"
    fi
    # quai-node's service, and the separate ones earlier releases installed.
    for name in quai-node quai-dash go-quai; do
        if [ "$(uname -s)" = Darwin ]; then
            plist="$HOME/Library/LaunchAgents/$LABEL_PREFIX$name.plist"
            if orphaned "$plist"; then act "$1" launchd "$plist" "$name"; fi
        else
            unit="/etc/systemd/system/$name.service"
            if orphaned "$unit"; then act "$1" systemd "$unit" "$name"; fi
            script="/etc/init.d/$name"
            if orphaned "$script"; then act "$1" openrc "$script" "$name"; fi
        fi
    done
}

# dangling: $1 is a symlink to a missing file whose path matches $2.
dangling() {
    [ -L "$1" ] && [ ! -e "$1" ] || return 1
    target="$(readlink "$1")"
    case "$target" in
        $2) return 0 ;;
    esac
    return 1
}

# orphaned: $1 is a service definition quai-node installed, and the program
# it runs no longer exists.
orphaned() {
    [ -f "$1" ] && grep -q "Installed by quai-node" "$1" 2>/dev/null || return 1
    prog="$(grep -oE "[^\"'<>=]*/bin/(quai-node|go-quai|quai-dash)" "$1" | head -n 1)"
    case "$prog" in
        /*) [ ! -e "$prog" ] ;;
        *) return 1 ;;
    esac
}

# act mode kind path [name]
act() {
    if [ "$1" = list ]; then
        case "$2" in
            link) say "$3" ;;
            *) say "$3 (the $4 service)" ;;
        esac
        return
    fi
    case "$2" in
        link)
            rm -f "$3"
            ;;
        launchd)
            launchctl bootout "gui/$(id -u)/$LABEL_PREFIX$4" >/dev/null 2>&1 || true
            rm -f "$3"
            ;;
        systemd)
            sudo systemctl disable --now "$4.service" >/dev/null 2>&1 || true
            sudo rm -f "$3"
            sudo systemctl daemon-reload
            ;;
        openrc)
            sudo rc-service "$4" stop >/dev/null 2>&1 || true
            sudo rc-update del "$4" default >/dev/null 2>&1 || true
            sudo rm -f "$3"
            ;;
    esac
}

main "$@"

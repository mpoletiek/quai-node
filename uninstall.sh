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

set -eu

BIN="quai-node"

say() { printf '%s\n' "$*"; }
fail() { printf 'uninstall: %s\n' "$*" >&2; exit 1; }

# Read in full before anything runs, so a download cut off halfway can't
# run half of it.
main() {
    [ "$(id -u)" != 0 ] || fail "run this as your own user, not root: quai-node asks for sudo itself to remove the service"
    binary="$HOME/.local/bin/$BIN"
    if [ ! -x "$binary" ]; then
        binary="$(command -v "$BIN" 2>/dev/null || true)"
        [ -n "$binary" ] || fail "no $BIN in ~/.local/bin or on your PATH; nothing to uninstall"
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

main "$@"

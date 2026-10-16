#!/bin/sh
# Install a Quai Network node with quai-node, from its GitHub releases.
#
#   curl -fsSL https://raw.githubusercontent.com/mpoletiek/quai-node/main/install.sh | sh
#
# It downloads the quai-node build for this computer, checks it against the
# release's SHA256SUMS, and runs `quai-node setup`, which asks a few
# questions and sets the node up: go-quai, the quai-dash dashboard, a
# background service, and `quai-node` and `quai-dash` in ~/.local/bin. Run
# it again to update; setup keeps your earlier answers. Don't run it as
# root: setup asks for sudo itself when it installs the service.
#
# Arguments after `sh -s --` go to `quai-node setup`, e.g. to update
# without questions:
#
#   curl -fsSL https://raw.githubusercontent.com/mpoletiek/quai-node/main/install.sh | sh -s -- --apply
#
# Settings, as environment variables (put them before `sh`):
#   QUAI_NODE_VERSION  a release tag to install (default: the newest release)
#
# Supported: Linux x86-64 and arm64, macOS on Apple Silicon and Intel.
# Windows: see install.ps1.

set -eu

REPO="mpoletiek/quai-node"
BIN="quai-node"

say() { printf '%s\n' "$*"; }
fail() { printf 'install: %s\n' "$*" >&2; exit 1; }

# The whole script is read before any of it runs, so a download cut off
# halfway can't run half an installer.
main() {
    need uname
    need mktemp
    [ "$(id -u)" != 0 ] || fail "run this as your own user, not root: quai-node asks for sudo itself when it installs the service"

    fetch_tool
    platform
    version="${QUAI_NODE_VERSION:-}"
    if [ -z "$version" ]; then
        version="$(newest_release)"
    fi
    case "$version" in
        v*) ;;
        *) version="v$version" ;;
    esac

    asset="$BIN-$version-$target"
    base="https://github.com/$REPO/releases/download/$version"
    work="$(mktemp -d)"
    trap 'rm -rf "$work"' EXIT INT TERM

    say "Downloading quai-node $version ($target)"
    download "$base/$asset" "$work/$asset" || fail "no $target build in release $version ($base/$asset)"
    download "$base/SHA256SUMS" "$work/SHA256SUMS" || fail "release $version has no SHA256SUMS; not running an unchecked download"
    verify "$work" "$asset"
    chmod 755 "$work/$asset"
    if [ "$(uname -s)" = Darwin ] && command -v xattr >/dev/null 2>&1; then
        # A download can be quarantined by Gatekeeper; this one was checked above.
        xattr -d com.apple.quarantine "$work/$asset" 2>/dev/null || true
    fi
    "$work/$asset" version >/dev/null 2>&1 || fail "the $target build of $version does not run on this system"

    say ""
    # Setup asks its questions on the terminal, not on the pipe this script
    # came through; it copies itself into its home, so the download can go.
    if [ -r /dev/tty ] && [ -w /dev/tty ] && (: </dev/tty) 2>/dev/null; then
        "$work/$asset" setup "$@" </dev/tty
    else
        "$work/$asset" setup "$@"
    fi
}

need() {
    command -v "$1" >/dev/null 2>&1 || fail "this needs '$1', which was not found"
}

fetch_tool() {
    if command -v curl >/dev/null 2>&1; then
        fetcher=curl
    elif command -v wget >/dev/null 2>&1; then
        fetcher=wget
    else
        fail "this needs curl or wget"
    fi
}

# download URL FILE
download() {
    if [ "$fetcher" = curl ]; then
        curl -fsSL --proto '=https' --tlsv1.2 --retry 3 -o "$2" "$1"
    else
        wget -q --https-only -O "$2" "$1"
    fi
}

platform() {
    os="$(uname -s)"
    arch="$(uname -m)"
    case "$os" in
        Linux)
            case "$arch" in
                x86_64 | amd64) target="linux-amd64" ;;
                aarch64 | arm64) target="linux-arm64" ;;
                *) fail "there is no Linux $arch build (only x86-64 and arm64)" ;;
            esac
            ;;
        Darwin)
            # A shell running under Rosetta reports x86_64 on Apple Silicon;
            # the native build is the one to install there.
            if [ "$arch" = arm64 ] || [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
                target="darwin-arm64"
            else
                target="darwin-amd64"
            fi
            ;;
        MINGW* | MSYS* | CYGWIN*)
            fail "on Windows, run this in PowerShell instead: irm https://raw.githubusercontent.com/$REPO/main/install.ps1 | iex"
            ;;
        *) fail "there is no build for $os; quai-node runs on Linux, macOS and Windows" ;;
    esac
}

# newest_release: the tag of the latest published release.
newest_release() {
    api="https://api.github.com/repos/$REPO/releases/latest"
    if [ "$fetcher" = curl ]; then
        body="$(curl -fsSL --proto '=https' --tlsv1.2 -H 'Accept: application/vnd.github+json' "$api")" || body=""
    else
        body="$(wget -q --https-only -O - "$api")" || body=""
    fi
    tag="$(printf '%s' "$body" | tr ',' '\n' | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
    [ -n "$tag" ] || fail "could not ask GitHub for the newest release (rate limited or offline?); set QUAI_NODE_VERSION to a tag, e.g. v0.1.0"
    printf '%s' "$tag"
}

# verify DIR FILE: FILE must appear in DIR/SHA256SUMS with a matching hash.
verify() {
    expected="$(awk -v f="$2" '$2 == f || $2 == "*" f { print $1 }' "$1/SHA256SUMS")"
    [ -n "$expected" ] || fail "SHA256SUMS does not list $2"
    if command -v sha256sum >/dev/null 2>&1; then
        actual="$(sha256sum "$1/$2" | awk '{ print $1 }')"
    elif command -v shasum >/dev/null 2>&1; then
        actual="$(shasum -a 256 "$1/$2" | awk '{ print $1 }')"
    else
        fail "found neither sha256sum nor shasum to check the download with; not running it unchecked"
    fi
    [ "$expected" = "$actual" ] || fail "checksum mismatch for $2 (expected $expected, got $actual); nothing was installed"
    say "Checksum verified."
}

main "$@"

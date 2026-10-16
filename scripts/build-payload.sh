#!/bin/sh
# Builds the payload quai-node embeds for one OS/arch:
# internal/bundle/payload/payload.tar.gz, holding go-quai (built from
# source, with cgo), its VERSION and params, and quai-dash when there is a
# build of it for the platform.
#
#   scripts/build-payload.sh GOOS GOARCH
#
# Environment:
#   GO_QUAI_REF      go-quai tag or branch to build (default: latest release tag)
#   GO_QUAI_SRC      use this go-quai checkout instead of cloning
#   DASH_VERSION     quai-dash release to bundle (default: latest; "none" to skip)
#   DASH_FROM_SOURCE 1: build quai-dash from source at DASH_VERSION when its
#                    release has no archive for the platform (needs cargo)
#   ZIG              zig binary for cross-compiling cgo (default: zig on PATH)
#
# Linux and Windows build anywhere zig runs. macOS needs a Mac (the
# Apple SDK isn't redistributable), where the system clang is used.
set -eu

goos=${1:?usage: build-payload.sh GOOS GOARCH}
goarch=${2:?usage: build-payload.sh GOOS GOARCH}
root=$(cd "$(dirname "$0")/.." && pwd)
work="$root/build/payload-$goos-$goarch"
out="$root/internal/bundle/payload/payload.tar.gz"
zig=${ZIG:-zig}

say() { printf '%s\n' "build-payload: $*" >&2; }
die() { say "$*"; exit 1; }

# latest REPO: the tag of REPO's latest published GitHub release. Not the
# newest tag: a tag can come before its release is published (or have
# none), and then its downloads don't exist yet.
latest() {
	curl -fsSL "https://api.github.com/repos/$1/releases/latest" |
		sed -n 's/^ *"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1
}

rm -rf "$work"
mkdir -p "$work/stage/params" "$root/build/src"

# ---------------------------------------------------------------- go-quai

if [ -n "${GO_QUAI_SRC:-}" ]; then
	src=$GO_QUAI_SRC
else
	ref=${GO_QUAI_REF:-}
	if [ -z "$ref" ]; then
		ref=$(latest dominant-strategies/go-quai)
		[ -n "$ref" ] || die "could not find go-quai's latest release"
	fi
	src="$root/build/src/go-quai-$ref"
	if [ ! -d "$src/.git" ]; then
		say "cloning go-quai $ref"
		git -c advice.detachedHead=false clone --quiet --depth 1 --branch "$ref" \
			https://github.com/dominant-strategies/go-quai "$src"
	fi
fi
commit=$(git -C "$src" rev-parse HEAD)
date=$(git -C "$src" log -1 --format=%cd --date=format:%Y%m%d)
version=$(cat "$src/VERSION")

# The C compiler for cgo: zig for Linux (static, musl) and Windows, from
# any host; the Mac's own clang for macOS.
case "$goos/$goarch" in
linux/amd64) zt=x86_64-linux-musl ;;
linux/arm64) zt=aarch64-linux-musl ;;
windows/amd64) zt=x86_64-windows-gnu ;;
windows/arm64) zt=aarch64-windows-gnu ;;
darwin/*) zt= ;;
*) die "unsupported target $goos/$goarch" ;;
esac
if [ -n "$zt" ]; then
	command -v "$zig" >/dev/null 2>&1 || die "zig is needed to build go-quai for $goos/$goarch (set ZIG)"
	cc="$zig cc -target $zt"
	cxx="$zig c++ -target $zt"
	extld='-extldflags=-static'
	[ "$goos" = windows ] && extld=
else
	[ "$(uname -s)" = Darwin ] || die "go-quai for macOS builds on a Mac only"
	cc=clang cxx=clang++ extld=
fi

exe=
[ "$goos" = windows ] && exe=.exe
say "building go-quai $version ($commit) for $goos/$goarch"
(
	cd "$src"
	CGO_ENABLED=1 GOOS=$goos GOARCH=$goarch CC=$cc CXX=$cxx \
		go build -trimpath \
		-ldflags "-s -w $extld -X main.gitCommit=$commit -X main.gitDate=$date" \
		-o "$work/stage/go-quai$exe" ./cmd/go-quai
)
cp "$src/VERSION" "$work/stage/VERSION"
cp "$src/params/genesis_alloc.json" "$src/params/forfeiture_addresses.json" "$work/stage/params/"

# --------------------------------------------------------------- quai-dash

dash_repo=mpoletiek/quai-node-dashboard
dash_version=${DASH_VERSION:-}
if [ "$dash_version" != none ]; then
	if [ -z "$dash_version" ]; then
		dash_version=$(latest "$dash_repo")
		[ -n "$dash_version" ] || die "could not find quai-dash's latest release"
		dash_version=${dash_version#v}
	fi
	case "$goarch" in amd64) darch=x86_64 ;; arm64) darch=aarch64 ;; esac
	case "$goos" in darwin) dos=macos ;; *) dos=$goos ;; esac
	name="quai-dash-$dash_version-$darch-$dos"
	base="https://github.com/$dash_repo/releases/download/v$dash_version"
	mkdir -p "$work/dash"
	if curl -fsSL -o "$work/dash/$name.tar.gz" "$base/$name.tar.gz" 2>/dev/null; then
		say "verifying $name.tar.gz against SHA256SUMS"
		curl -fsSL -o "$work/dash/SHA256SUMS" "$base/SHA256SUMS"
		(
			cd "$work/dash"
			want=$(awk -v f="$name.tar.gz" '$2 == f || $2 == "*"f {print $1}' SHA256SUMS)
			[ -n "$want" ] || die "$name.tar.gz is not in SHA256SUMS"
			if command -v sha256sum >/dev/null 2>&1; then
				got=$(sha256sum "$name.tar.gz" | cut -d' ' -f1)
			else
				got=$(shasum -a 256 "$name.tar.gz" | cut -d' ' -f1)
			fi
			[ "$got" = "$want" ] || die "$name.tar.gz: checksum mismatch"
			tar -xzf "$name.tar.gz"
		)
		cp "$work/dash/$name/quai-dash$exe" "$work/stage/quai-dash$exe"
	elif [ "${DASH_FROM_SOURCE:-0}" = 1 ]; then
		say "no quai-dash $dash_version archive for $goos/$goarch; building it from source"
		git -c advice.detachedHead=false clone --quiet --depth 1 --branch "v$dash_version" \
			"https://github.com/$dash_repo" "$work/dash/src"
		case "$goos/$goarch" in
		darwin/amd64) rt=x86_64-apple-darwin ;;
		darwin/arm64) rt=aarch64-apple-darwin ;;
		windows/amd64) rt=x86_64-pc-windows-msvc ;;
		*) die "no Rust target known for $goos/$goarch" ;;
		esac
		# The target goes to the toolchain quai-dash pins (rust-toolchain.toml),
		# so it's added from inside its checkout.
		if command -v rustup >/dev/null 2>&1; then
			(cd "$work/dash/src" && rustup target add "$rt")
		fi
		(cd "$work/dash/src" && cargo build --locked --release --target "$rt")
		cp "$work/dash/src/target/$rt/release/quai-dash$exe" "$work/stage/quai-dash$exe"
	else
		say "no quai-dash $dash_version archive for $goos/$goarch; the payload carries none"
		dash_version=
	fi
else
	dash_version=
fi

# ----------------------------------------------------------------- payload

cat >"$work/stage/manifest.json" <<EOF
{
  "goos": "$goos",
  "goarch": "$goarch",
  "go_quai_version": "$version",
  "go_quai_commit": "$commit",
  "dash_version": "$dash_version"
}
EOF
chmod 0755 "$work/stage/go-quai$exe"
[ -f "$work/stage/quai-dash$exe" ] && chmod 0755 "$work/stage/quai-dash$exe"
tar -czf "$out" -C "$work/stage" .
say "wrote $out ($(du -h "$out" | cut -f1)): go-quai $version${dash_version:+, quai-dash $dash_version}"

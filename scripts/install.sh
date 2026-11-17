#!/bin/sh
# Install decsr from the latest GitHub release (or DECSR_VERSION).
#
#   curl -fsSL https://raw.githubusercontent.com/Decsr/Decsr-Code/main/scripts/install.sh | sh
#
# Environment:
#   DECSR_VERSION      release tag to install, e.g. v0.7.0 (default: latest)
#   DECSR_INSTALL_DIR  install directory (default: $HOME/.local/bin)
set -eu

REPO="Decsr/Decsr-Code"
BIN="decsr"

err() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"
}

need uname
need tar
need mktemp

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
	fetch_stdout() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q "$1" -O "$2"; }
	fetch_stdout() { wget -qO- "$1"; }
else
	err "curl or wget is required"
fi

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) err "unsupported OS: $(uname -s) (supported: linux, darwin; on Windows download the zip from https://github.com/$REPO/releases)" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) err "unsupported architecture: $(uname -m) (supported: amd64, arm64)" ;;
esac

tag="${DECSR_VERSION:-}"
if [ -z "$tag" ]; then
	tag=$(fetch_stdout "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
	[ -n "$tag" ] || err "could not determine the latest release (GitHub API rate limit? set DECSR_VERSION, e.g. DECSR_VERSION=v0.7.0)"
fi
case "$tag" in
v*) ;;
*) tag="v$tag" ;;
esac
version="${tag#v}"

archive="${BIN}_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
	err "sha256sum or shasum is required to verify the download"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

printf 'Downloading %s %s (%s/%s)...\n' "$BIN" "$tag" "$os" "$arch"
fetch "$base/$archive" "$tmp/$archive" || err "no release asset $archive for $tag (see https://github.com/$REPO/releases)"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || err "could not download checksums.txt for $tag"

want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || err "$archive not listed in checksums.txt"
got=$(sha256 "$tmp/$archive")
[ "$want" = "$got" ] || err "checksum mismatch for $archive (expected $want, got $got)"

mkdir "$tmp/x"
tar -xzf "$tmp/$archive" -C "$tmp/x" || err "could not extract $archive"
[ -f "$tmp/x/$BIN" ] || err "$BIN not found in $archive"

dir="${DECSR_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$dir" || err "cannot create $dir (set DECSR_INSTALL_DIR to a writable directory)"
if command -v install >/dev/null 2>&1; then
	install -m 755 "$tmp/x/$BIN" "$dir/$BIN" || err "cannot write to $dir"
else
	cp "$tmp/x/$BIN" "$dir/$BIN" && chmod 755 "$dir/$BIN" || err "cannot write to $dir"
fi

printf 'Installed %s %s to %s/%s\n' "$BIN" "$tag" "$dir" "$BIN"

case ":$PATH:" in
*":$dir:"*) ;;
*)
	printf '\n%s is not on your PATH. Add it with:\n  export PATH="%s:$PATH"\n' "$dir" "$dir"
	;;
esac
printf '\nNext: run `decsr doctor` to check your setup.\n'

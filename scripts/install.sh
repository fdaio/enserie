#!/bin/sh
# Install the ens client from a GitHub release.
#
# The script detects the operating system and architecture, downloads the
# matching tarball, verifies it against SHA256SUMS, and installs the binary.
# It does not use sudo.
#
# Usage:
#   curl -fsSL .../install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --version 0.3.1
#   curl -fsSL .../install.sh | sh -s -- --prefix "$HOME/.local"
#
# ENS_BASE_URL overrides the release URL. The test suite uses it to serve a
# local release.

set -eu

usage() {
	cat <<'EOF'
Install the ens client.

Usage: install.sh [--version VERSION] [--prefix DIR]

  --version VERSION  install this release instead of the latest one
  --prefix DIR       install under DIR/bin (default: $HOME/.local)
  -h, --help         show this help

The installer downloads from GitHub Releases and verifies SHA256SUMS.
It installs the ens client only and never invokes sudo.
EOF
}

die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

download() {
	download_url=$1
	download_dest=$2
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$download_url" -o "$download_dest" || die "could not download $download_url"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$download_dest" "$download_url" || die "could not download $download_url"
	else
		die "need curl or wget"
	fi
}

sha256_file() {
	sha_file=$1
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$sha_file" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$sha_file" | awk '{print $1}'
	else
		die "need sha256sum or shasum"
	fi
}

VERSION=
PREFIX=
BASE_URL=${ENS_BASE_URL:-https://github.com/fdaio/enserie/releases}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--version)
			[ "$#" -ge 2 ] || die "--version needs a value"
			VERSION=${2#v}
			shift 2
			;;
		--prefix)
			[ "$#" -ge 2 ] || die "--prefix needs a value"
			PREFIX=$2
			shift 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			die "unknown option: $1"
			;;
	esac
done

if [ -n "$VERSION" ]; then
	case "$VERSION" in
		[0-9]*) ;;
		*) die "invalid version: $VERSION" ;;
	esac
	case "$VERSION" in
		*[!0-9A-Za-z._-]*) die "invalid version: $VERSION" ;;
	esac
fi

if [ -z "$PREFIX" ]; then
	[ -n "${HOME:-}" ] || die "HOME is not set; pass --prefix"
	PREFIX=$HOME/.local
fi
PREFIX=${PREFIX%/}
[ -n "$PREFIX" ] || die "--prefix must name a directory"
BASE_URL=${BASE_URL%/}

case "$(uname -s)" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) die "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
	x86_64|amd64) arch=amd64 ;;
	arm64|aarch64) arch=arm64 ;;
	*) die "unsupported architecture: $(uname -m)" ;;
esac

if [ -n "$VERSION" ]; then
	release_url=$BASE_URL/download/v$VERSION
else
	release_url=$BASE_URL/latest/download
fi
asset=ens-$os-$arch.tar.gz
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/ens-install.XXXXXX")
cleanup() {
	[ -n "${tmp_bin:-}" ] && rm -f "$tmp_bin"
	[ -n "${tmp_dir:-}" ] && rm -rf "$tmp_dir"
}
trap cleanup EXIT HUP INT TERM

download "$release_url/SHA256SUMS" "$tmp_dir/SHA256SUMS"
download "$release_url/$asset" "$tmp_dir/$asset"

expected=$(awk -v name="$asset" '$2 == name { print $1; exit }' "$tmp_dir/SHA256SUMS")
[ -n "$expected" ] || die "SHA256SUMS has no entry for $asset"
actual=$(sha256_file "$tmp_dir/$asset")
[ "$actual" = "$expected" ] || die "checksum mismatch for $asset"

tar -xzf "$tmp_dir/$asset" -C "$tmp_dir" ens || die "could not extract $asset"
chmod 755 "$tmp_dir/ens"
installed=$("$tmp_dir/ens" version) || die "downloaded binary did not run"
if [ -n "$VERSION" ] && [ "$installed" != "$VERSION" ]; then
	die "downloaded version is $installed, expected $VERSION"
fi

bin_dir=$PREFIX/bin
mkdir -p "$bin_dir" || die "could not create $bin_dir"
tmp_bin=$bin_dir/.ens.$$
cp "$tmp_dir/ens" "$tmp_bin" || die "could not write $bin_dir"
chmod 755 "$tmp_bin"
mv "$tmp_bin" "$bin_dir/ens" || die "could not install $bin_dir/ens"

printf 'Installed ens %s at %s\n' "$installed" "$bin_dir/ens"
case ":${PATH:-}:" in
	*":$bin_dir:"*) ;;
	*) printf "Add %s to PATH, for example:\n  export PATH=\"%s:\$PATH\"\n" "$bin_dir" "$bin_dir" ;;
esac

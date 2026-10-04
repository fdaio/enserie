#!/bin/sh
# Build a macOS installer package from one binary.
#
# pkgbuild ships with macOS, so this needs nothing the release runner lacks.
# The package installs into /usr/local/bin and accepts either architecture:
# a package that refuses a wrong-architecture binary at install time is worse
# than one that installs it.
set -eu

if [ "$#" -lt 4 ]; then
	echo "usage: pack-pkg.sh VERSION ARCH BINARY OUT [NAME] [SUMMARY]" >&2
	exit 2
fi

VERSION=$1
ARCH=$2
BINARY=$3
OUT=$4
NAME=${5:-enserie.pkg}
SUMMARY=${6:-enserie, a P2P overlay network}

case "$ARCH" in
amd64) PKG_ARCH=x86_64 ;;
arm64) PKG_ARCH=arm64 ;;
*)
	echo "unsupported pkg arch: $ARCH" >&2
	exit 2
	;;
esac

if [ ! -f "$BINARY" ]; then
	echo "missing binary: $BINARY" >&2
	exit 2
fi

if ! command -v pkgbuild >/dev/null 2>&1; then
	echo "pkgbuild is required to pack $OUT; it ships with macOS" >&2
	exit 1
fi

STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT

mkdir -p "$STAGE/root/usr/local/bin"
chmod 0755 "$STAGE/root" "$STAGE/root/usr" "$STAGE/root/usr/local" "$STAGE/root/usr/local/bin"
cp "$BINARY" "$STAGE/root/usr/local/bin/ens"
chmod 0755 "$STAGE/root/usr/local/bin/ens"

mkdir -p "$(dirname "$OUT")"
# Resolve OUT after mkdir so pkgbuild can write a relative path.
pkgbuild --root "$STAGE/root" \
	--identifier "dev.getfda.enserie.ens" \
	--version "$VERSION" \
	--install-location / \
	"$OUT"

# pkgbuild reports what it produced. Let a caller confirm the architecture
# instead of taking the file name on trust.
echo "packed $(basename "$OUT") for $PKG_ARCH" >&2
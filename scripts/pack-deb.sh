#!/bin/sh
# Build a simple .deb from one statically linked binary.
# Requires dpkg-deb (present on Debian/Ubuntu and in GitHub-hosted Linux runners).
set -eu

if [ "$#" -lt 4 ]; then
	echo "usage: pack-deb.sh VERSION ARCH BINARY OUT [PACKAGE] [SUMMARY]" >&2
	exit 2
fi

VERSION=$1
ARCH=$2
BINARY=$3
OUT=$4
PACKAGE=${5:-enserie}
SUMMARY=${6:-P2P overlay network (QUIC first, relay fallback)}

case "$ARCH" in
amd64 | arm64) ;;
*)
	echo "unsupported deb arch: $ARCH" >&2
	exit 2
	;;
esac

if [ ! -f "$BINARY" ]; then
	echo "missing binary: $BINARY" >&2
	exit 2
fi

if ! command -v dpkg-deb >/dev/null 2>&1; then
	echo "dpkg-deb is required to pack $OUT" >&2
	exit 1
fi

STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT

mkdir -p "$STAGE/usr/bin" "$STAGE/DEBIAN"
chmod 0755 "$STAGE" "$STAGE/usr" "$STAGE/usr/bin" "$STAGE/DEBIAN"
cp "$BINARY" "$STAGE/usr/bin/$PACKAGE"
chmod 0755 "$STAGE/usr/bin/$PACKAGE"

SIZE=$(du -sk "$STAGE/usr" | awk '{print $1}')

cat >"$STAGE/DEBIAN/control" <<EOF
Package: $PACKAGE
Version: $VERSION
Section: net
Priority: optional
Architecture: $ARCH
Maintainer: fdaio <noreply@getfda.dev>
Homepage: https://github.com/fdaio/enserie
Installed-Size: $SIZE
Description: $SUMMARY
EOF

mkdir -p "$(dirname "$OUT")"
# Resolve OUT after mkdir so dpkg-deb can write a relative path.
dpkg-deb --root-owner-group --build "$STAGE" "$OUT"

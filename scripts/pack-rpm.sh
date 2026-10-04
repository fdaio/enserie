#!/bin/sh
# Build a minimal .rpm from one statically linked binary.
#
# Uses rpmbuild so the spec stays in this file, which keeps the rpm metadata
# next to the deb metadata in pack-deb.sh instead of adding a build tool.
# rpmbuild is present in GitHub-hosted Linux runners; install the rpm package
# elsewhere.
set -eu

if [ "$#" -lt 4 ]; then
	echo "usage: pack-rpm.sh VERSION ARCH BINARY OUT [PACKAGE] [SUMMARY]" >&2
	exit 2
fi

VERSION=$1
ARCH=$2
BINARY=$3
OUT=$4
PACKAGE=${5:-enserie}
SUMMARY=${6:-P2P overlay network (QUIC first, relay fallback)}

# rpm names its architectures x86_64 and aarch64, and distro rpm only knows the
# host one. BuildArch must name the rpm spelling while the file names keep the
# Go spelling.
case "$ARCH" in
amd64)
	RPM_ARCH=x86_64
	;;
arm64)
	RPM_ARCH=aarch64
	;;
*)
	echo "unsupported rpm arch: $ARCH" >&2
	exit 2
	;;
esac

if ! command -v rpmbuild >/dev/null 2>&1; then
	echo "rpmbuild is required to pack $OUT" >&2
	exit 1
fi

# Catching this here turns rpmbuild's "No compatible architectures found" into
# a message that says what to do about it.
HOST_ARCH=$(rpm --eval '%{_build_arch}' 2>/dev/null || echo unknown)
if [ "$HOST_ARCH" != "$RPM_ARCH" ]; then
	echo "cannot pack an $RPM_ARCH rpm on a $HOST_ARCH host: distro rpm builds for the host architecture only" >&2
	echo "  run this on a $RPM_ARCH runner" >&2
	exit 2
fi

if [ ! -f "$BINARY" ]; then
	echo "missing binary: $BINARY" >&2
	exit 2
fi

# The %install script runs in the directory _topdir/BUILD points at, so a
# relative path would not resolve to the binary the caller passed.
BINARY=$(cd "$(dirname "$BINARY")" && pwd)/$(basename "$BINARY")

TOP=$(mktemp -d)
trap 'rm -rf "$TOP"' EXIT

# rpm rejects a release tag it cannot parse, and a bare 1.0 is the convention.
RELEASE=1
# Build day in the format rpm wants, such as "Mon Oct 04 2026". Deriving it
# keeps the spec valid whatever day the release runs.
CHANGELOG_DATE=$(date -u '+%a %b %d %Y')

# Version must be [0-9.] plus letters, and a dash is illegal, so a prerelease
# tag such as v0.3.0-rc1 cannot be packed. Say so here, because the failure
# rpmbuild reports otherwise names the spec rather than the tag.
case "$VERSION" in
*[!0-9a-zA-Z.]* | '' | .* | *..* | *.)
	echo "rpm cannot use version $VERSION: only digits, letters, and dots are allowed" >&2
	echo "  a prerelease tag such as v0.3.0-rc1 needs the dash dropped" >&2
	exit 2
	;;
esac

cat >"$TOP/ens.spec" <<EOF
Name:           $PACKAGE
Version:        $VERSION
Release:        $RELEASE%{?dist}
Summary:        $SUMMARY
License:        Apache-2.0
URL:            https://github.com/fdaio/enserie
BuildArch:      $RPM_ARCH

%description
$SUMMARY

%prep

%build

%install
mkdir -p %{buildroot}%{_bindir}
install -m 0755 $BINARY %{buildroot}%{_bindir}/$PACKAGE

%files
%{_bindir}/$PACKAGE

%changelog
* $CHANGELOG_DATE fdaio <noreply@getfda.dev> - $VERSION-1
- Release $VERSION
EOF

mkdir -p "$(dirname "$OUT")"
# Distro rpm registers only the host architecture, so a spec whose BuildArch
# is not the host one fails with "No compatible architectures found". There is
# no flag that fixes it: the release workflow builds each rpm on a runner of
# its own architecture, x86_64 on ubuntu-latest and aarch64 on
# ubuntu-24.04-arm. Read the rpm back from _topdir rather than overriding
# _rpmdir, which rpmbuild treats as one flat directory and then disagrees with
# its own output.
rpmbuild --define "_topdir $TOP/build" \
	--buildroot "$TOP/buildroot" \
	-bb "$TOP/ens.spec" >"$TOP/rpmbuild.log" 2>&1 || {
		cat "$TOP/rpmbuild.log" >&2
		exit 1
	}

PACKAGED=$(find "$TOP/build/RPMS" -name "$PACKAGE-*.rpm" | head -1)
if [ -z "$PACKAGED" ]; then
	echo "rpmbuild produced no rpm for $PACKAGE" >&2
	cat "$TOP/rpmbuild.log" >&2
	exit 1
fi
cp "$PACKAGED" "$OUT"
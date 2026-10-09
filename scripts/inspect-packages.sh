#!/bin/sh
# Check that the packages in dist/ carry the client binary.
#
# Both CI and the release workflow run this, so a check cannot pass in CI and
# then fail on a tag. It has bitten twice: the release workflow once named an
# rpm without its version, and once looked for a line in the Homebrew formula
# that brew audit had asked to remove.
#
# Usage: inspect-packages.sh VERSION ARCH PLATFORM [FORMAT ...]
#   VERSION   the version the packages were built with, such as 0.2.10
#   ARCH      amd64 or arm64, the Go spelling
#   PLATFORM  linux or darwin
#   FORMAT    deb, rpm, tarball, pkg, formula. Defaults to every format the
#             platform supports.
#
# The formats are named rather than detected. Inferring them from the tools
# present made the result depend on what happened to be installed on the
# runner, which is how a missing rpm went unnoticed on a job that never
# builds one.
set -eu

if [ "$#" -lt 3 ]; then
	echo "usage: inspect-packages.sh VERSION ARCH PLATFORM [FORMAT ...]" >&2
	exit 2
fi

VERSION=$1
ARCH=$2
PLATFORM=$3
shift 3

case "$ARCH" in
amd64) RPM_ARCH=x86_64 ;;
arm64) RPM_ARCH=aarch64 ;;
*)
	echo "unsupported arch: $ARCH" >&2
	exit 2
	;;
esac

if [ "$#" -eq 0 ]; then
	case "$PLATFORM" in
	linux) set -- deb rpm tarball ;;
	darwin) set -- tarball pkg formula ;;
	*) fail "unknown platform: $PLATFORM" ;;
	esac
fi

DEB="dist/ens_${VERSION}_${ARCH}.deb"
RPM="dist/ens_${VERSION}_${ARCH}.rpm"
LINUX_TARBALL="dist/ens-linux-${ARCH}.tar.gz"
DARWIN_TARBALL="dist/ens-darwin-${ARCH}.tar.gz"
PKG="dist/ens_${VERSION}_${ARCH}.pkg"

fail() {
	echo "inspect: $*" >&2
	exit 1
}

# The client package must carry the client and nothing else. A package that
# also shipped the relay would put a server binary on every user's PATH.
# The label names the package, because the listing command does not.
check_client_only() {
	label=$1
	shift
	if "$@" | grep -q enserie-relay; then
		fail "$label contains enserie-relay"
	fi
}

# Report a missing package before reporting a missing tool, so that a name
# mistake reads as a name mistake.
need() {
	[ -f "$1" ] || fail "missing $1"
}

case "$PLATFORM" in
linux)
	for format in "$@"; do
		case "$format" in
		deb)
			need "$DEB"
			command -v dpkg-deb >/dev/null || fail "dpkg-deb not found"
			dpkg-deb -c "$DEB" | grep -q usr/bin/ens || fail "$DEB has no usr/bin/ens"
			check_client_only "$DEB" dpkg-deb -c "$DEB"
			;;
		rpm)
			need "$RPM"
			command -v rpm >/dev/null || fail "rpm not found"
			# A matrix that produced two packages of one architecture would
			# look fine here unless the architecture is named.
			got=$(rpm -qp --qf '%{ARCH}' "$RPM")
			[ "$got" = "$RPM_ARCH" ] || fail "$RPM is $got, want $RPM_ARCH"
			rpm -qp --qf '%{FILENAMES}\n' "$RPM" | grep -qx /usr/bin/ens \
				|| fail "$RPM has no /usr/bin/ens"
			;;
		tarball)
			need "$LINUX_TARBALL"
			tar -tzf "$LINUX_TARBALL" | grep -qx ens \
				|| fail "$LINUX_TARBALL has no ens at the top level"
			;;
		*)
			fail "unknown linux format: $format"
			;;
		esac
	done
	;;
darwin)
	for format in "$@"; do
		case "$format" in
		tarball)
			need "$DARWIN_TARBALL"
			command -v tar >/dev/null || fail "tar not found"
			tar -tzf "$DARWIN_TARBALL" | grep -qx ens \
				|| fail "$DARWIN_TARBALL has no ens at the top level"
			;;
		pkg)
			need "$PKG"
			command -v pkgutil >/dev/null || fail "pkgutil not found"
			stage=$(mktemp -d)
			trap 'rm -rf "$stage"' EXIT
			pkgutil --expand "$PKG" "$stage/pkg" >/dev/null
			grep -q "version=\"${VERSION}\"" "$stage/pkg/PackageInfo" \
				|| fail "$PKG does not carry version ${VERSION}"
			grep -q 'identifier="dev.getfda.enserie.ens"' "$stage/pkg/PackageInfo" \
				|| fail "$PKG has the wrong identifier"
			# The payload is what lands on disk, so read the paths out of it
			# rather than trusting the install location alone.
			paths=$(gunzip -dc "$stage/pkg/Payload" 2>/dev/null | cpio -t 2>/dev/null || true)
			echo "$paths" | grep -qx './usr/local/bin/ens' \
				|| fail "$PKG payload has no ./usr/local/bin/ens"
			check_client_only "$PKG" printf '%s\n' "$paths"
			;;
		formula)
			# The formula names one tarball per platform and architecture. A
			# stale or missing entry fails only for whoever tries to install,
			# so check all four here.
			need dist/ens.rb
			check_formula_entry() {
				entry=$1
				tarball="dist/ens-$entry.tar.gz"
				need "$tarball"
				grep -q "v${VERSION}/ens-$entry.tar.gz" dist/ens.rb \
					|| fail "dist/ens.rb does not name the v${VERSION} $entry tarball"
				if command -v sha256sum >/dev/null; then
					want=$(sha256sum "$tarball" | cut -d' ' -f1)
				else
					want=$(shasum -a 256 "$tarball" | cut -d' ' -f1)
				fi
				grep -q "$want" dist/ens.rb \
					|| fail "dist/ens.rb checksum for $entry is stale"
			}
			for entry in \
				darwin-arm64 darwin-amd64 linux-arm64 linux-amd64
			do
				check_formula_entry "$entry"
			done
			# brew style is what a tap submission is checked against.
			command -v brew >/dev/null || fail "brew not found"
			brew style dist/ens.rb >/dev/null || fail "dist/ens.rb fails brew style"
			;;
		*)
			fail "unknown darwin format: $format"
			;;
		esac
	done
	;;
*)
	fail "unknown platform: $PLATFORM"
	;;
esac

echo "inspect: ${PLATFORM} ${ARCH} v${VERSION} [$*] ok"

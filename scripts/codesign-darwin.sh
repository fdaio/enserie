#!/bin/sh
# Ad-hoc sign Mach-O binaries produced on macOS.
# Linux-cross darwin binaries fail Apple strict validation and cannot be signed.
set -eu

if [ "$(uname -s)" != Darwin ]; then
	echo "codesign-darwin.sh must run on macOS" >&2
	exit 1
fi

if [ "$#" -lt 1 ]; then
	echo "usage: codesign-darwin.sh BINARY..." >&2
	exit 2
fi

for f in "$@"; do
	if [ ! -f "$f" ]; then
		echo "missing binary: $f" >&2
		exit 2
	fi
	id="com.fdaio.$(basename "$f")"
	codesign -s - --force --identifier "$id" "$f"
	codesign --verify "$f"
done

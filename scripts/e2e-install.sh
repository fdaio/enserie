#!/bin/bash
# End-to-end check of the install script.
#
# The test serves a small fake release over local HTTP. It exercises the
# default prefix, the latest and pinned URLs, the checksum check, and a piped
# invocation.

set -euo pipefail

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/ens-install-e2e.XXXXXX")
HTTP_PID=

cleanup() {
  if [ -n "$HTTP_PID" ]; then
    kill "$HTTP_PID" 2>/dev/null || true
    disown "$HTTP_PID" 2>/dev/null || true
  fi
  rm -rf "$TMP"
}
trap cleanup EXIT

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "unsupported test OS: $(uname -s)" >&2; exit 2 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "unsupported test architecture: $(uname -m)" >&2; exit 2 ;;
esac
asset="ens-${os}-${arch}.tar.gz"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

make_release() {
  dir=$1
  mkdir -p "$dir"
  cp "$TMP/$asset" "$dir/$asset"
  printf '%s  %s\n' "$(sha256_file "$dir/$asset")" "$asset" >"$dir/SHA256SUMS"
}

make_bad_release() {
  dir=$1
  mkdir -p "$dir"
  cp "$TMP/$asset" "$dir/$asset"
  printf '%064d  %s\n' 0 "$asset" >"$dir/SHA256SUMS"
}

echo "== prepare a fake release"
mkdir -p "$TMP/work"
cat >"$TMP/work/ens" <<'EOF'
#!/bin/sh
echo "0.3.1"
EOF
chmod 755 "$TMP/work/ens"
tar -C "$TMP/work" -czf "$TMP/$asset" ens

make_release "$TMP/release/latest/download"
make_release "$TMP/release/download/v0.3.1"
make_bad_release "$TMP/release/bad/latest/download"

echo "== start a local HTTP server"
(cd "$TMP/release" && python3 -u -m http.server 0 --bind 127.0.0.1) >"$TMP/http.log" 2>&1 &
HTTP_PID=$!
PORT=
for _ in $(seq 1 200); do
  PORT=$(sed -n 's/.*port \([0-9][0-9]*\).*/\1/p' "$TMP/http.log" | head -1)
  [ -n "$PORT" ] && break
  sleep 0.1
done
if [ -z "$PORT" ]; then
  echo "the HTTP server did not report a port" >&2
  cat "$TMP/http.log" >&2
  exit 1
fi
echo "  port $PORT"
BASE="http://127.0.0.1:$PORT"
export NO_PROXY="127.0.0.1,localhost"
export no_proxy="$NO_PROXY"

echo "== default prefix"
mkdir -p "$TMP/home"
log=$(HOME="$TMP/home" ENS_BASE_URL="$BASE" "$ROOT/scripts/install.sh" 2>&1)
printf '%s\n' "$log" | sed 's/^/  /'
printf '%s\n' "$log" | grep -Fq "Add $TMP/home/.local/bin to PATH"
[ "$("$TMP/home/.local/bin/ens" version)" = "0.3.1" ]

echo "== explicit prefix"
log=$(ENS_BASE_URL="$BASE" "$ROOT/scripts/install.sh" --prefix "$TMP/prefix" 2>&1)
printf '%s\n' "$log" | sed 's/^/  /'
printf '%s\n' "$log" | grep -Fq "Add $TMP/prefix/bin to PATH"
[ "$("$TMP/prefix/bin/ens" version)" = "0.3.1" ]

echo "== pinned version"
ENS_BASE_URL="$BASE" "$ROOT/scripts/install.sh" --version v0.3.1 --prefix "$TMP/prefix-pinned" >/dev/null
[ "$("$TMP/prefix-pinned/bin/ens" version)" = "0.3.1" ]

echo "== piped invocation"
ENS_BASE_URL="$BASE" sh -s -- --prefix "$TMP/prefix-piped" <"$ROOT/scripts/install.sh" >/dev/null
[ "$("$TMP/prefix-piped/bin/ens" version)" = "0.3.1" ]

echo "== missing HOME"
if env -u HOME ENS_BASE_URL="$BASE" "$ROOT/scripts/install.sh" >"$TMP/nohome.log" 2>&1; then
  echo "installer accepted a missing HOME" >&2
  exit 1
fi
grep -Fq 'HOME is not set' "$TMP/nohome.log"

echo "== bad checksum"
if ENS_BASE_URL="$BASE/bad" "$ROOT/scripts/install.sh" --prefix "$TMP/prefix-bad" >"$TMP/bad.log" 2>&1; then
  echo "installer accepted a bad checksum" >&2
  exit 1
fi
grep -Fq 'checksum mismatch' "$TMP/bad.log"
[ ! -e "$TMP/prefix-bad/bin/ens" ]

echo "PASS: install.sh installed a verified release without root"

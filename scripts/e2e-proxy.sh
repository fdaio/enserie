#!/bin/bash
# End-to-end check of the zero-privilege SOCKS5 proxy mode.
#
# Two workers run as the same unprivileged user with separate TMPDIRs, so each
# has its own lock and log. A local HTTP server stands in for a service on the
# peer. The test reaches it through the other node's SOCKS5 listener and the
# peer's overlay address, which proxy mode maps to loopback. No TUN, no sudo.
#
# Usage: scripts/e2e-proxy.sh
#   ENS_BIN=/path/to/ens  test a binary that is already built.

set -euo pipefail

if [ "$(id -u)" -eq 0 ]; then
  echo "run this test as a normal user; proxy mode must not need root" >&2
  exit 2
fi

log() { printf '== %s\n' "$*"; }

free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}

RUN_DIR=$(mktemp -d "${TMPDIR:-/tmp}/ens-proxy-e2e.XXXXXX")
HTTP_PID=
INVITE_PID=
ACCEPT_PID=

cleanup() {
  if [ -n "${RUN_DIR:-}" ] && [ -x "$RUN_DIR/ens" ]; then
    TMPDIR="$RUN_DIR/a" "$RUN_DIR/ens" down >/dev/null 2>&1 || true
    TMPDIR="$RUN_DIR/z" "$RUN_DIR/ens" down >/dev/null 2>&1 || true
  fi
  if [ -n "$INVITE_PID" ]; then
    kill "$INVITE_PID" 2>/dev/null || true
    disown "$INVITE_PID" 2>/dev/null || true
  fi
  if [ -n "$ACCEPT_PID" ]; then
    kill "$ACCEPT_PID" 2>/dev/null || true
    disown "$ACCEPT_PID" 2>/dev/null || true
  fi
  if [ -n "$HTTP_PID" ]; then
    kill "$HTTP_PID" 2>/dev/null || true
    disown "$HTTP_PID" 2>/dev/null || true
  fi
  [ -n "${RUN_DIR:-}" ] && rm -rf "$RUN_DIR"
}
trap cleanup EXIT

mkdir -p "$RUN_DIR/a" "$RUN_DIR/z" "$RUN_DIR/www"
echo "proxy-e2e-ok" >"$RUN_DIR/www/index.html"

if [ -n "${ENS_BIN:-}" ]; then
  log "use the prebuilt binary ${ENS_BIN}"
  [ -x "$ENS_BIN" ] || { echo "ENS_BIN is not executable: ${ENS_BIN}" >&2; exit 2; }
  cp "$ENS_BIN" "$RUN_DIR/ens"
else
  log "build"
  go build -o "$RUN_DIR/ens" ./cmd/ens
fi

log "start a local HTTP server"
(cd "$RUN_DIR/www" && python3 -u -m http.server 0 --bind 127.0.0.1) >"$RUN_DIR/http.log" 2>&1 &
HTTP_PID=$!
HTTP_PORT=
for _ in $(seq 1 100); do
  HTTP_PORT=$(sed -n 's/.*port \([0-9][0-9]*\).*/\1/p' "$RUN_DIR/http.log" | head -1)
  [ -n "$HTTP_PORT" ] && break
  sleep 0.1
done
[ -n "$HTTP_PORT" ] || { echo "the HTTP server did not report a port" >&2; cat "$RUN_DIR/http.log" >&2; exit 1; }
echo "  port ${HTTP_PORT}"

SOCKS_A=$(free_port)
SOCKS_Z=$(free_port)

log "node A invites in proxy mode"
TMPDIR="$RUN_DIR/a" "$RUN_DIR/ens" invite --socks-listen "127.0.0.1:$SOCKS_A" \
  >"$RUN_DIR/invite.out" 2>"$RUN_DIR/invite.err" &
INVITE_PID=$!
for _ in $(seq 1 100); do
  grep -q 'ens accept' "$RUN_DIR/invite.out" 2>/dev/null && break
  sleep 0.1
done
sed 's/^/  /' "$RUN_DIR/invite.err"
token=$(sed -n -e 's/^sudo .*ens accept //p' -e 's/^ens accept //p' "$RUN_DIR/invite.out" | head -1)
[ -n "$token" ] || { echo "no invite token appeared" >&2; cat "$RUN_DIR/invite.out" >&2; exit 1; }
overlay_a=$(sed -n 's/.*overlay \([0-9.]*\).*/\1/p' "$RUN_DIR/invite.err" | head -1)
[ -n "$overlay_a" ] || { echo "could not read node A's overlay address" >&2; exit 1; }

log "node Z accepts in proxy mode"
TMPDIR="$RUN_DIR/z" "$RUN_DIR/ens" accept --socks-listen "127.0.0.1:$SOCKS_Z" "$token" \
  >"$RUN_DIR/accept.out" 2>"$RUN_DIR/accept.err" &
ACCEPT_PID=$!
if ! wait "$ACCEPT_PID"; then
  echo "node Z did not report a path" >&2
  sed 's/^/  /' "$RUN_DIR/accept.err" >&2
  exit 1
fi
ACCEPT_PID=
sed 's/^/  /' "$RUN_DIR/accept.err"
overlay_z=$(sed -n 's/.*overlay \([0-9.]*\).*/\1/p' "$RUN_DIR/accept.err" | head -1)
[ -n "$overlay_z" ] || { echo "could not read node Z's overlay address" >&2; exit 1; }
if ! wait "$INVITE_PID"; then
  echo "node A did not report a path" >&2
  sed 's/^/  /' "$RUN_DIR/invite.err" >&2
  exit 1
fi
INVITE_PID=

grep -q 'ens: proxy' "$RUN_DIR/invite.err" || { echo "node A did not report proxy mode" >&2; exit 1; }
if grep -q 'ens: tun' "$RUN_DIR/invite.err" "$RUN_DIR/accept.err"; then
  echo "a node opened a TUN in proxy mode" >&2
  exit 1
fi

log "status reports a user worker without sudo"
status=$(TMPDIR="$RUN_DIR/a" "$RUN_DIR/ens" status)
echo "$status" | sed 's/^/  /'
echo "$status" | grep -q 'as this user' || { echo "status did not name a user worker" >&2; exit 1; }
if echo "$status" | grep -q 'sudo '; then
  echo "status asked for sudo for a user worker" >&2
  exit 1
fi

log "A -> Z through the proxy"
body=$(curl -sS --max-time 15 --socks5-hostname "127.0.0.1:$SOCKS_A" "http://$overlay_z:$HTTP_PORT/")
echo "$body" | grep -q 'proxy-e2e-ok' || { echo "A -> Z returned the wrong body: $body" >&2; exit 1; }

log "Z -> A through the proxy"
body=$(curl -sS --max-time 15 --socks5-hostname "127.0.0.1:$SOCKS_Z" "http://$overlay_a:$HTTP_PORT/")
echo "$body" | grep -q 'proxy-e2e-ok' || { echo "Z -> A returned the wrong body: $body" >&2; exit 1; }

log "stop both workers without sudo"
down_a=$(TMPDIR="$RUN_DIR/a" "$RUN_DIR/ens" down 2>&1)
down_z=$(TMPDIR="$RUN_DIR/z" "$RUN_DIR/ens" down 2>&1)
echo "  $down_a"
echo "  $down_z"
echo "$down_a" | grep -q 'stopped pid' || { echo "node A did not stop" >&2; exit 1; }
echo "$down_z" | grep -q 'stopped pid' || { echo "node Z did not stop" >&2; exit 1; }

log "PASS: proxy mode carried traffic in both directions without root"

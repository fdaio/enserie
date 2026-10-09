#!/bin/bash
# End-to-end check of the overlay data plane on Linux.
#
# Two network namespaces stand in for two machines. A veth pair joins them and
# each namespace runs one ens node, so a packet has to cross a real TUN in both
# directions. That covers TUN creation, address and route setup, packet
# forwarding, and the splice, none of which the unit tests reach.
#
# The nodes reach each other directly over the veth pair, so this exercises the
# QUIC path only. The relay path has no such check, because the command line
# takes no relay option and the default relays are hosted, so a check against
# them would depend on someone else's uptime. Run it as root: it creates
# namespaces and a veth pair. The nodes themselves run as NODE_USER with
# CAP_NET_ADMIN, because one instance lock per host keeps two nodes in two
# namespaces apart.
#
# Usage: sudo scripts/e2e-overlay.sh [--subnet CIDR]
#   --subnet CIDR  take the overlay link from this network, e.g. 10.99.0.0/24.
#                  Use it when a VPN or another network already holds the
#                  default range.
#
# Set ENS_BIN to test a binary that is already built. CI uses this because
# sudo drops the Go toolchain from PATH.

set -euo pipefail

NS_A=ens-a
NS_Z=ens-z
LINK_A=ov-a
LINK_Z=ov-z
V4_A=10.200.0.1
V4_Z=10.200.0.2
RUN_DIR=/tmp/ens-e2e
ACCEPT_TIMEOUT=90
SUBNET=""
NODE_USER="${NODE_USER:-$(stat -c %U .)}"
NODE_UID=$(id -u "$NODE_USER")
NODE_GID=$(id -g "$NODE_USER")

[ "$(id -u)" = 0 ] || { echo "run this as root" >&2; exit 2; }
while [ $# -gt 0 ]; do
  case "$1" in
    --subnet)
      [ $# -ge 2 ] || { echo "--subnet needs a network, e.g. --subnet 10.99.0.0/24" >&2; exit 2; }
      SUBNET="$2"
      shift 2
      ;;
    --subnet=*) SUBNET="${1#--subnet=}"; shift ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

for tool in ip ping setpriv; do
  command -v "$tool" >/dev/null || { echo "missing tool: $tool" >&2; exit 2; }
done
[ -c /dev/net/tun ] || { echo "missing /dev/net/tun" >&2; exit 2; }
log() { printf '\n=== %s\n' "$*"; }

for ns in "$NS_A" "$NS_Z"; do
  if ip netns list 2>/dev/null | grep -qE "^${ns} "; then
    echo "namespace ${ns} already exists, remove it first" >&2
    exit 2
  fi
done

teardown() {
  set +e
  for ns in "$NS_A" "$NS_Z"; do
    ip netns list 2>/dev/null | grep -qE "^${ns} " || continue
    ip netns pids "$ns" 2>/dev/null | xargs -r kill 2>/dev/null
    ip netns del "$ns" 2>/dev/null
  done
  ip link del "$LINK_A" 2>/dev/null
  rm -rf "$RUN_DIR"
}
trap teardown EXIT

# The node runs unprivileged, so it needs CAP_NET_ADMIN for the TUN and the
# route commands. Its instance lock lives under TMPDIR, which keeps the two
# namespaces from blocking each other. Capabilities come from setpriv because
# /tmp is commonly mounted nosuid, where a file capability would be ignored.
node() {
  local ns=$1 dir=$2
  shift 2
  ip netns exec "$ns" setpriv \
    --reuid="$NODE_UID" --regid="$NODE_GID" --clear-groups \
    --inh-caps=+net_admin --ambient-caps=+net_admin \
    env TMPDIR="$dir" "$@"
}

rm -rf "$RUN_DIR"
mkdir -p "$RUN_DIR/a" "$RUN_DIR/z"
if [ -n "${ENS_BIN:-}" ]; then
  log "use the prebuilt binary ${ENS_BIN}"
  [ -x "$ENS_BIN" ] || { echo "ENS_BIN is not executable: ${ENS_BIN}" >&2; exit 2; }
  cp "$ENS_BIN" "$RUN_DIR/ens"
else
  log "build"
  go build -o "$RUN_DIR/ens" ./cmd/ens
fi
chown -R "$NODE_USER" "$RUN_DIR"

log "namespaces and veth pair"
ip netns add "$NS_A"
ip netns add "$NS_Z"
ip link add "$LINK_A" type veth peer name "$LINK_Z"
ip link set "$LINK_A" netns "$NS_A"
ip link set "$LINK_Z" netns "$NS_Z"
ip -n "$NS_A" addr add "${V4_A}/24" dev "$LINK_A"
ip -n "$NS_Z" addr add "${V4_Z}/24" dev "$LINK_Z"
for ns in "$NS_A" "$NS_Z"; do ip -n "$ns" link set lo up; done
ip -n "$NS_A" link set "$LINK_A" up
ip -n "$NS_Z" link set "$LINK_Z" up
ip netns exec "$NS_A" ping -c1 -W2 "$V4_Z" >/dev/null && echo "veth path works between the namespaces"

log "node A invites${SUBNET:+ from ${SUBNET}}"
invite_args=()
[ -n "$SUBNET" ] && invite_args=(--subnet "$SUBNET")
node "$NS_A" "$RUN_DIR/a" "$RUN_DIR/ens" invite "${invite_args[@]}" \
  >"$RUN_DIR/invite.out" 2>"$RUN_DIR/invite.err" &
for _ in $(seq 1 100); do
  grep -q 'ens accept' "$RUN_DIR/invite.out" 2>/dev/null && break
  sleep 0.2
done
sed 's/^/  /' "$RUN_DIR/invite.err"
# The invite line names the binary by absolute path, because that is the only
# form of "sudo ens" a Homebrew install can run. Match the token after the
# subcommand rather than after a fixed prefix.
token=$(sed -n 's/^sudo .*ens accept //p' "$RUN_DIR/invite.out" | head -1)
[ -n "$token" ] || { echo "no invite token appeared" >&2; exit 1; }
overlay_a=$(sed -n 's/.*overlay \([0-9.]*\).*/\1/p' "$RUN_DIR/invite.err" | head -1)

log "node Z accepts"
if ! node "$NS_Z" "$RUN_DIR/z" timeout "$ACCEPT_TIMEOUT" "$RUN_DIR/ens" accept "$token" \
  >"$RUN_DIR/accept.out" 2>"$RUN_DIR/accept.err"; then
  echo "node Z did not report a path within ${ACCEPT_TIMEOUT}s" >&2
  sed 's/^/  /' "$RUN_DIR/accept.err" >&2
  exit 1
fi
sed 's/^/  /' "$RUN_DIR/accept.err"
overlay_z=$(sed -n 's/.*overlay \([0-9.]*\).*/\1/p' "$RUN_DIR/accept.err" | head -1)
echo "  node A overlay: ${overlay_a:-unknown}"
echo "  node Z overlay: ${overlay_z:-unknown}"
[ -n "$overlay_a" ] && [ -n "$overlay_z" ] || { echo "could not read the overlay addresses" >&2; exit 1; }

log "path in use"
path=$(grep -h "path " "$RUN_DIR"/*.err | sed 's/.*path //' | sort -u | paste -sd, -)
echo "  ${path:-unknown}"
[ "${path:-}" = quic ] || echo "  note: expected quic over the veth pair"

if [ -n "$SUBNET" ]; then
  # The address itself is the check: it must be the one the node was told to
  # use, and the node's own kernel must route it without leaving the host.
  # A point-to-point TUN keeps that address on the local route, so accept both.
  routed=$(ip netns exec "$NS_A" ip -4 route get "$overlay_a" 2>/dev/null | head -1)
  case "$routed" in
    *"src $overlay_a"*)
      case "$routed" in
        *"dev enserie"*|*"dev lo"*) ;;
        *)
          echo "overlay ${overlay_a} is routed off-host as ${SUBNET}" >&2
          echo "  ${routed}" >&2
          exit 1
          ;;
      esac
      ;;
    *)
      echo "overlay ${overlay_a} is not routed as ${SUBNET}" >&2
      echo "  ${routed:-no route}" >&2
      exit 1
      ;;
  esac
  echo "  overlay ${overlay_a} sits in the requested ${SUBNET}"
fi

log "ping A -> Z"
ip netns exec "$NS_A" ping -c3 -W3 "$overlay_z"

log "ping Z -> A"
ip netns exec "$NS_Z" ping -c3 -W3 "$overlay_a"

log "interfaces"
ip -n "$NS_A" addr show dev enserie0 | grep -E "^[0-9]+:|inet " | sed 's/^/  /'
ip -n "$NS_Z" addr show dev enserie0 | grep -E "^[0-9]+:|inet " | sed 's/^/  /'

log "PASS: the overlay carries traffic in both directions over ${path:-an unknown path}"
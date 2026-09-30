# enserie

P2P overlay network: QUIC first, relay as fallback.

Two peers take the two addresses of an IPv4 `/30` and reach each other as if
they were on a LAN. Direct QUIC is preferred. When NAT blocks it, a blind
WebSocket relay splices the path. Overlay packets stay inside TLS 1.3.

```
go get github.com/fdaio/enserie
```

tyd is unchanged and does not import this module yet.

## Install

GitHub Releases ship Ubuntu `.deb` files and macOS tarballs. There is no apt
repository and no Homebrew formula.

Ubuntu / Debian (amd64 or arm64):

```bash
ver=0.1.1
arch=$(dpkg --print-architecture)
curl -fsSL -o enserie.deb \
  "https://github.com/fdaio/enserie/releases/download/v${ver}/enserie_${ver}_${arch}.deb"
sudo dpkg -i enserie.deb
enserie --version
```

macOS (replace `arm64` with `amd64` on Intel). Use v0.1.1 or later:
v0.1.0 darwin tarballs were cross-compiled on Linux and Gatekeeper
SIGKILLs them.

```bash
ver=0.1.1
curl -fsSL "https://github.com/fdaio/enserie/releases/download/v${ver}/enserie-darwin-arm64.tar.gz" | tar -xz
sudo install -m 755 enserie /usr/local/bin/enserie
enserie --version
```

The client package contains only `enserie`. The relay is a separate download
(`enserie-relay_*.deb` or `enserie-relay-*.tar.gz`) for people who run their
own splice. Most nodes can reuse tyd's hosted relays:

```
https://relay-1.getfda.dev
https://relay-2.getfda.dev
```

Those URLs are the CLI default. `--relay off` turns fallback off.
`--relay http://127.0.0.1:9090` points at a local `enserie-relay`.

## Overlay

Each node brings:

- a node id
- a local overlay CIDR (`10.7.0.1/30`)
- the peer id, peer overlay IP (`10.7.0.2`), and peer cert fingerprint
- optional QUIC candidates
- optional relay URLs (defaults above)

The process opens a TUN, assigns the local overlay IP, and forwards IPv4
packets whose destination is the peer.

TUN setup needs privilege (`CAP_NET_ADMIN` or root).

## Commands

```bash
enserie-relay --listen 127.0.0.1:9090

# node A
sudo enserie --id a --ip 10.7.0.1/30 --peer z=10.7.0.2 \
  --peer-fp <Z_FP> --dir /var/lib/enserie/a \
  --relay http://127.0.0.1:9090

# node Z
sudo enserie --id z --ip 10.7.0.2/30 --peer a=10.7.0.1 \
  --peer-fp <A_FP> --dir /var/lib/enserie/z \
  --relay http://127.0.0.1:9090 --peer-addr <A_QUIC>
```

Start each node once without `--peer-fp` to print its fingerprint, then restart
with the peer value. `ping 10.7.0.2` from A should then reach Z.

`--force-relay` skips QUIC. `--relay off` disables fallback; dial then fails if
QUIC cannot connect.

## Build

```bash
make test
make build
make dist-linux VERSION=0.1.1    # .deb; needs dpkg-deb
make dist-darwin VERSION=0.1.1   # macOS tarballs; must run on macOS
```

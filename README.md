# enserie

P2P overlay network: QUIC first, relay as fallback. The command is `ens`.

Two machines share an IPv4 `/30` and reach each other as if they were on a LAN.
Direct QUIC is preferred. When NAT blocks it, a blind WebSocket relay splices
the path. Overlay packets stay inside TLS 1.3.

```
go get github.com/fdaio/enserie
```

## Install

GitHub Releases ship Ubuntu `.deb` files and macOS tarballs. Use v0.2.6 or
later for background `ens invite`.

Ubuntu / Debian (amd64 or arm64):

```bash
ver=0.2.6
arch=$(dpkg --print-architecture)
curl -fsSL -o ens.deb \
  "https://github.com/fdaio/enserie/releases/download/v${ver}/ens_${ver}_${arch}.deb"
sudo dpkg -i ens.deb
ens version
```

macOS (replace `arm64` with `amd64` on Intel):

```bash
ver=0.2.6
curl -fsSL "https://github.com/fdaio/enserie/releases/download/v${ver}/ens-darwin-arm64.tar.gz" | tar -xz
sudo install -m 755 ens /usr/local/bin/ens
ens version
```

TUN setup needs root (`CAP_NET_ADMIN`). Relay traffic reuses tyd's hosted
splices (`relay-1.getfda.dev`, `relay-2.getfda.dev`). Pack `enserie-relay`
only if you run your own splice.

## Commands

On machine A:

```bash
sudo ens invite
```

It prints a one-line command. On machine Z, paste it:

```bash
sudo ens accept <token>
```

The two hosts pick a `/30`, exchange identities, and connect. QUIC is tried
first. Relay is automatic when QUIC cannot complete.

After the first path is up, the parent prints a pid and returns the shell.
The worker keeps the TUN. `sudo ens down` stops it. Later path logs go to
`/run/ens.log`.

When the path is up, `ping` the overlay IP printed on the other side.

## Build

```bash
make test
make build
make dist-linux VERSION=0.2.6
make dist-darwin VERSION=0.2.6
```

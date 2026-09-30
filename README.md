# enserie

P2P overlay network: QUIC first, relay as fallback.

Two peers take the two addresses of an IPv4 `/30` and reach each other as if
they were on a LAN. Direct QUIC is preferred. When NAT blocks it, a blind
WebSocket relay splices the path. Overlay packets stay inside TLS 1.3.

```
go get github.com/fdaio/enserie
```

tyd is unchanged and does not import this module yet.

## Overlay

Each node brings:

- a node id
- a local overlay CIDR (`10.7.0.1/30`)
- the peer id, peer overlay IP (`10.7.0.2`), and peer cert fingerprint
- optional QUIC candidates
- optional relay URLs

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

`--force-relay` skips QUIC. `--relay off` (or omit `--relay` and omit
`--peer-addr`) disables fallback; dial then fails if QUIC cannot connect.

## Build

```bash
make test
make build
```

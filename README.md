# enserie

P2P overlay network: QUIC first, relay as fallback. The command is `ens`.

Two machines share an IPv4 `/30` and reach each other as if they were on a LAN.
Direct QUIC is preferred. When NAT blocks it, a blind WebSocket relay splices
the path. Overlay packets stay inside TLS 1.3.

`docs/protocol.md` describes the pairing, the token, both paths, and the trust
boundaries. `SECURITY.md` describes what the token does and does not protect.

## Install

GitHub Releases ship Ubuntu `.deb` files and macOS tarballs. The snippets below
install v0.2.9; background `ens invite` needs v0.2.6 or later.

Ubuntu / Debian (amd64 or arm64):

```bash
ver=0.2.9
arch=$(dpkg --print-architecture)
curl -fsSL -o ens.deb \
  "https://github.com/fdaio/enserie/releases/download/v${ver}/ens_${ver}_${arch}.deb"
sudo dpkg -i ens.deb
ens version
```

macOS (replace `arm64` with `amd64` on Intel):

```bash
ver=0.2.9
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

By default the link comes from `198.18.0.0/15`, the range RFC 2544 reserves for
benchmarks. Pass `--subnet` to choose another range:

```bash
sudo ens invite --subnet 10.99.0.0/24
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

### When a VPN takes the range

A VPN can route the overlay away from the TUN, and the symptom is quiet: the
path comes up, `ens` reports `connected via quic` or `connected via relay`, and
the overlay IP then drops every packet. Cloudflare WARP does this on Linux, for
example, because its policy rules outrank the main routing table.

The packet never reaches the interface, so `ping` shows no reply and the TUN
counters stay flat. Check it with `ip route get <peer overlay IP>`: the answer
must name the TUN, such as `enserie0`, and not a tunnel. Passing `--subnet` with
a range the VPN does not claim is the way out.

### Known limits

Overlay packets travel in an ordered QUIC stream, not in datagrams. One lost
segment therefore holds up the packets behind it, and the inner IP layer and
the QUIC layer both run congestion control. That is fine for a command line
between two machines, and it costs throughput on a busy or lossy path. QUIC
datagrams would remove both effects and are the natural change if that matters.

The interface MTU is fixed at 1280, chosen to avoid fragmentation on the
underlay. The relay caps a control message at 64 KiB.

A path is chosen once per connection and does not change afterwards. A link that
starts on the relay stays on the relay even after the network stops blocking
direct QUIC, because the node keeps the first peer it accepted rather than
migrating to a second one.

## Use as a library

Three packages, one job each. `overlay` runs the node, `transport` carries QUIC
and certificates, `relay` serves a splice of your own. `go get` on its own
starts nothing: your program owns the process, the certificates, and the
pairing, so it does the work that `ens` does around the node.

```bash
go get github.com/fdaio/enserie@v0.2.9
```

### Pairing

Pairing is the same out-of-band token exchange that `ens invite` prints. The
inviting node only listens and offers, so it must not dial. The accepting node
dials, even when its own id sorts first.

The snippets skip the errors that the generators cannot fail on. Check the rest.

```go
// Inviting node.
id, _ := overlay.RandomID()
secret, _ := overlay.RandomSecret()
link, _ := overlay.RandomLink() // e.g. 10.7.0.1/30
peerCIDR, _ := overlay.OtherCIDR(link)
peerIP, _, _ := net.ParseCIDR(peerCIDR) // 10.7.0.2
_, prefix, _ := net.ParseCIDR(link)
cert, _ := transport.EnsureServerCert("tls.crt", "tls.key")
fp, _ := transport.CertFingerprint(cert)

n, err := overlay.New(overlay.Config{
	ID:     id,
	CIDR:   link,
	Cert:   cert,
	Listen: "0.0.0.0:0",
	Relays: overlay.DefaultRelayURLs(),
	Peer:   overlay.Peer{IP: peerIP},
	Secret: secret,
	Invite: true,
})
if err != nil {
	log.Fatal(err)
}
if err := n.Start(ctx); err != nil { // opens a TUN: needs root or CAP_NET_ADMIN
	log.Fatal(err)
}
defer n.Close()

token, _ := overlay.Invite{
	V:      1,
	ID:     id,
	FP:     fp,
	CIDR:   link,
	Addrs:  overlay.FilterOverlayAddrs(transport.ExpandCandidates(n.ListenAddr(), ""), prefix),
	Secret: secret,
}.Encode()
fmt.Println("share with the peer:", token)
```

```go
// Accepting node.
inv, err := overlay.ParseInvite(token)
if err != nil {
	log.Fatal(err)
}
link, _ := overlay.OtherCIDR(inv.CIDR)
peerIP, _, _ := net.ParseCIDR(inv.CIDR)
id, _ := overlay.RandomID()
cert, _ := transport.EnsureServerCert("tls.crt", "tls.key")

n, err := overlay.New(overlay.Config{
	ID:         id,
	CIDR:       link,
	Cert:       cert,
	Listen:     "0.0.0.0:0",
	Relays:     inv.Relays,
	Peer:       overlay.Peer{ID: inv.ID, IP: peerIP, CertFP: inv.FP, Candidates: inv.Addrs},
	Secret:     inv.Secret,
	AlwaysDial: true,
})
if err != nil {
	log.Fatal(err)
}
if err := n.Start(ctx); err != nil {
	log.Fatal(err)
}
defer n.Close()

for n.Path() == "" { // quic first, relay when QUIC cannot complete
	time.Sleep(200 * time.Millisecond)
}
fmt.Println("peer overlay IP:", n.PeerIP())
```

### What the token carries

The token is base64 JSON. The accepting node pins the inviter by the
fingerprint in the token. The inviter authenticates the accepting node by the
shared secret, because it cannot know a fingerprint in advance.
`ParseInvite` rejects a wrong version or a missing field, and fills `Relays`
with the hosted splices when the token omits them.

- `n.Path()` reports `transport.KindQUIC` or `transport.KindRelay` once a path is up.
- `n.ListenAddr()` plus `transport.ExpandCandidates` give the addresses to share.
- `Config.Device` takes a packet source and sink, so a test can run without a
  TUN. `Node.WaitPacket` only reads the memory device in this package, so it
  stays inside these tests.
- `ens` adds what the library leaves to you: one instance per host
  (`/run/ens.lock`), a background worker with a pid, and `/run/ens.log`.

## Build

```bash
make test
make build
make dist-linux VERSION=0.2.9
make dist-darwin VERSION=0.2.9
```

# enserie

P2P overlay network: QUIC first, relay as fallback. The command is `ens`.

Two machines share an IPv4 `/30` and reach each other as if they were on a LAN.
Direct QUIC is preferred. When NAT blocks it, a blind WebSocket relay splices
the path. Overlay packets stay inside TLS 1.3.

`docs/protocol.md` describes the pairing, the token, both paths, and the trust
boundaries. `SECURITY.md` describes what the token does and does not protect.

## Install

GitHub Releases ship packages for both platforms. The snippets below install
v0.2.14; background `ens invite` needs v0.2.6 or later.

Every package installs the client only. The relay is separate, and you need it
only if you run your own splice.

macOS, with Homebrew:

```bash
brew tap fdaio/enserie
brew install fdaio/enserie/ens
ens version
```

macOS, from the installer package (double-click also works):

```bash
ver=0.2.14
case $(uname -m) in
arm64) pkg_arch=arm64 ;;
x86_64) pkg_arch=amd64 ;;
esac
curl -fsSL -o ens.pkg \
  "https://github.com/fdaio/enserie/releases/download/v${ver}/ens_${ver}_${pkg_arch}.pkg"
sudo installer -pkg ens.pkg -target /
ens version
```

macOS, from the tarball (replace `arm64` with `amd64` on Intel):

```bash
ver=0.2.14
curl -fsSL "https://github.com/fdaio/enserie/releases/download/v${ver}/ens-darwin-arm64.tar.gz" | tar -xz
sudo install -m 755 ens /usr/local/bin/ens
ens version
```

Ubuntu / Debian (amd64 or arm64):

```bash
ver=0.2.14
arch=$(dpkg --print-architecture)
curl -fsSL -o ens.deb \
  "https://github.com/fdaio/enserie/releases/download/v${ver}/ens_${ver}_${arch}.deb"
sudo dpkg -i ens.deb
ens version
```

Fedora, RHEL, openSUSE (amd64 or arm64):

```bash
ver=0.2.14
arch=$(uname -m)   # x86_64 or aarch64
case "$arch" in
x86_64) rpm_arch=amd64 ;;
aarch64) rpm_arch=arm64 ;;
esac
curl -fsSL -o ens.rpm \
  "https://github.com/fdaio/enserie/releases/download/v${ver}/ens_${ver}_${rpm_arch}.rpm"
sudo dnf install -y ./ens.rpm || sudo rpm -i ens.rpm
ens version
```

Any other Linux, from the tarball. The binary is statically linked, so it runs
on Alpine and on distributions without a package of their own:

```bash
ver=0.2.14
case $(uname -m) in
x86_64 | amd64) goarch=amd64 ;;
aarch64 | arm64) goarch=arm64 ;;
esac
curl -fsSL "https://github.com/fdaio/enserie/releases/download/v${ver}/ens-linux-${goarch}.tar.gz" | tar -xz
sudo install -m 755 ens /usr/local/bin/ens
ens version
```

There is no Arch package. Arch is not Debian-based and the AUR is maintained by
the community, so the tarball above is the route. Writing a PKGBUILD is one
small file if you want to add it.

TUN setup needs root (`CAP_NET_ADMIN`). Relay traffic reuses tyd's hosted
splices (`relay-1.getfda.dev`, `relay-2.getfda.dev`). Pack `enserie-relay`
only if you run your own splice.

Verify a download against the release checksum before running it:

```bash
ver=0.2.14
curl -fsSL -O "https://github.com/fdaio/enserie/releases/download/v${ver}/SHA256SUMS"
shasum -a 256 -c SHA256SUMS
```

The file lists every asset, so check the ones you downloaded:

```bash
shasum -a 256 -c SHA256SUMS --ignore-missing
```

Read the output: `OK` per file is what you want. `--ignore-missing` skips the
assets you did not download, and the exit code stays 0 as long as every file
that *is* present matches.

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

The other machine dials the addresses in the token, in order, and each miss
waits out a timeout before the next. They are ordered by how likely they are to
answer: a public address first, then a LAN address, then a VPN or carrier NAT
address such as `100.64.x.x`, which only answers if the peer is on the same
VPN. Nothing is dropped, so two machines on one LAN still pair directly.

When you know which address the peer can reach, name it. It is tried first:

```bash
sudo ens invite --advertise 203.0.113.7
sudo ens invite --advertise home.example.com
```

That is the fix for a host whose only reachable address is a port mapping or a
name the other side can resolve.

It prints a one-line command. On machine Z, paste it:

```bash
sudo ens accept <token>
```

The two hosts pick a `/30`, exchange identities, and connect. QUIC is tried
first. Relay is automatic when QUIC cannot complete.

After the first path is up, the parent prints a pid and returns the shell.
The worker keeps the TUN. `sudo ens down` stops it. Later path logs go to
`/run/ens.log`, or `/var/run/ens.log` on macOS, which has no `/run`.

Installing a new version does not change a running one. A package manager
replaces the file on disk, and the running worker keeps the code it started
with, so a worker that is stuck keeps the bug it was started with. If `ens
down` reports that a pid ignored SIGTERM, or the overlay will not come up after
an upgrade, force the old process out:

```bash
sudo pkill -f 'ens (invite|accept)'
sudo ip link delete enserie0   # only if a TUN is left behind
```

Then `sudo ens invite` starts on the new binary.

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
go get github.com/fdaio/enserie@v0.2.14
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
  (`/run/ens.lock`, or `/var/run/ens.lock` on macOS), a background worker with
  a pid, and a log next to it.

## Build

```bash
make test
make build
make dist-linux VERSION=0.2.14
make dist-darwin VERSION=0.2.14
```

## License

Apache-2.0 — see [LICENSE](LICENSE).

# enserie

A2Z overlay network: QUIC first, relay as fallback. The command is `ens`.

Two machines share an IPv4 `/30`. In TUN mode they reach each other as if they
were on a LAN. In the default proxy mode they reach each other's TCP services
through a local SOCKS5 listener. Direct QUIC is preferred. When NAT blocks it, a
blind WebSocket relay splices the path. Traffic stays inside TLS 1.3.

By default `ens` runs a local SOCKS5 proxy and needs no root. `ens invite --tun`
adds a transparent TUN interface and needs root or `CAP_NET_ADMIN`.

`docs/protocol.md` describes the pairing, the token, both paths, and the trust
boundaries. `SECURITY.md` describes what the token does and does not protect.

## Install

The snippets below install v0.2.18; background `ens invite` needs v0.2.6 or
later. Every package installs the client only; the relay is separate, and you
need it only if you run your own splice.

Proxy mode is new. The v0.2.18 packages still default to TUN; build from
`main` to use the zero-privilege mode until the next release.

With Homebrew, on macOS or Linux, on Apple Silicon or Intel:

```bash
brew tap fdaio/enserie
brew install fdaio/enserie/ens
```

Without Homebrew, set the version and pick your platform. The download URL is
the same for every package; only the architecture differs.

```bash
ver=0.2.18
rel=https://github.com/fdaio/enserie/releases/download/v${ver}
arch=arm64   # or amd64 on Intel
```

| Platform | Command |
|---|---|
| macOS | `curl -fsSLO "$rel/ens_${ver}_${arch}.pkg"`<br>`sudo installer -pkg "ens_${ver}_${arch}.pkg" -target /` |
| Debian, Ubuntu | `curl -fsSLO "$rel/ens_${ver}_$(dpkg --print-architecture).deb"`<br>`sudo dpkg -i "ens_${ver}_$(dpkg --print-architecture).deb"` |
| Fedora, RHEL, openSUSE | `curl -fsSLO "$rel/ens_${ver}_${arch}.rpm"`<br>`sudo dnf install -y "ens_${ver}_${arch}.rpm"` |
| Any other Linux | `curl -fsSL "$rel/ens-linux-${arch}.tar.gz" \| tar -xz`<br>`sudo install -m 755 ens /usr/local/bin/` |

On Debian and Ubuntu, `dpkg --print-architecture` already prints `amd64` or
`arm64`, so that row needs no `arch`. Elsewhere `uname -m` prints `x86_64` or
`aarch64`, which is why those rows need it set by hand.

The Linux tarball is statically linked, so it also runs on Alpine and on any
distribution without a package of its own.

Double-clicking the macOS `.pkg` installs the same way.

There is no Arch package. Arch is not Debian-based and the AUR is maintained by
the community, so the tarball above is the route. Writing a PKGBUILD is one
small file if you want to add it.

To check a download before running it:

```bash
curl -fsSLO "$rel/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing
```

Each file you downloaded should report `OK`. `--ignore-missing` skips the assets
you did not download, and the exit code stays 0 as long as every file that is
present matches. Homebrew checks the checksum for you. On macOS, use
`shasum -a 256` in place of `sha256sum`.

Proxy mode runs as your user and needs no root. TUN mode (`--tun`) needs root or
`CAP_NET_ADMIN`. Relay traffic uses tyd's hosted splices (`relay-1.getfda.dev`,
`relay-2.getfda.dev`).

## Commands

The default mode is a local SOCKS5 proxy. It needs no root and carries TCP
through the peer. On machine A:

```bash
ens invite
```

The command prints a SOCKS5 address (default `127.0.0.1:1080`) and a token.
On machine Z, paste the token:

```bash
ens accept <token>
```

Both machines print the SOCKS5 address. Use it with any client that speaks
SOCKS5, for example:

```bash
curl --socks5-hostname 127.0.0.1:1080 http://<peer-overlay-ip>:8080/
ssh -o ProxyCommand='nc -X 5 -x 127.0.0.1:1080 %h %p' user@<peer-overlay-ip>
```

A target that names the peer's overlay IP is mapped to loopback on the peer, so
`<peer-overlay-ip>:8080` reaches a service on the peer's `127.0.0.1:8080`.
Other targets are dialed from the peer as given. The proxy carries TCP only;
UDP and ICMP are not implemented.

`--socks-listen` changes the local address. Keep it on loopback unless you
trust every host that can reach the port:

```bash
ens invite --socks-listen 127.0.0.1:1081
ens accept --socks-listen 127.0.0.1:1082 <token>
```

The link still comes from `198.18.0.0/15`, the range RFC 2544 reserves for
benchmarks. Pass `--subnet` to choose another range:

```bash
ens invite --subnet 10.99.0.0/24
```

The other machine dials the addresses in the token, in order, and each miss
waits out a timeout before the next. They are ordered by how likely they are to
answer: a public address first, then a LAN address, then a VPN or carrier NAT
address such as `100.64.x.x`, which only answers if the peer is on the same
VPN. Nothing is dropped, so two machines on one LAN still pair directly.

When you know which address the peer can reach, name it. It is tried first:

```bash
ens invite --advertise 203.0.113.7
ens invite --advertise home.example.com
```

That is the fix for a host whose only reachable address is a port mapping or a
name the other side can resolve.

The two hosts exchange identities and connect. QUIC is tried first. Relay is
automatic when QUIC cannot complete.

After the first path is up, the parent prints a pid and returns the shell. The
worker keeps the SOCKS5 listener. `ens down` stops it. In proxy mode the log is
`$TMPDIR/ens.log`; on Linux that is usually `/tmp/ens.log`, and on macOS it is
under the per-user temporary directory.

`ens status` reports what is running and shows the tail of that log. It reads
the lock and the log rather than asking the worker, so it works without root
even when the worker runs as root:

```console
$ ens status
  state:   running (pid 16467, as this user)
  log:     /tmp/ens.log

/tmp/ens down stops it.
```

The startup lines with the SOCKS5 address and the first path stay on the
terminal. Later path changes and proxy errors go to the log.

It exits 1 and prints `ens: not running` when no worker is up, so a script can
ask without parsing the output. The fields come from the lock file and the log,
so `ens status` does not need to talk to the worker.

### TUN mode

Use `--tun` when an application cannot be pointed at a SOCKS5 proxy and you
need transparent IP. It opens a TUN interface, so it needs root or
`CAP_NET_ADMIN`:

```bash
sudo ens invite --tun
sudo ens accept <token>
```

The accept side reads the mode from the token, so the command it prints is
`sudo ens accept <token>`. After the path is up, `ping` the overlay IP printed
on the other side. The worker keeps the TUN. `sudo ens down` stops it. The log
is `/run/ens.log`, or `/var/run/ens.log` on macOS.

The `ens status` example above is proxy mode. A root TUN worker is reported as
`as root` and the stop command starts with `sudo`.

Installing a new version does not change a running one. A package manager
replaces the file on disk, and the running worker keeps the code it started
with, so a worker that is stuck keeps the bug it was started with. If `ens
down` reports that a pid ignored SIGTERM, or the overlay will not come up after
an upgrade, force the old process out:

```bash
pkill -f 'ens (invite|accept)'          # proxy mode
sudo pkill -f 'ens (invite|accept)'     # TUN mode
sudo ip link delete enserie0            # only if a TUN is left behind
```

Then start the new binary again.

### When a VPN takes the range

This applies to TUN mode. Proxy mode has no TUN route.

A VPN can route the overlay away from the TUN, and the symptom is quiet: the
path comes up, `ens` reports `connected via quic` or `connected via relay`, and
the overlay IP then drops every packet. Cloudflare WARP does this on Linux, for
example, because its policy rules outrank the main routing table.

The packet never reaches the interface, so `ping` shows no reply and the TUN
counters stay flat. Check it with `ip route get <peer overlay IP>`: the answer
must name the TUN, such as `enserie0`, and not a tunnel. Passing `--subnet` with
a range the VPN does not claim is the way out.

### Known limits

Proxy mode carries TCP streams, not IP packets. UDP ASSOCIATE and ICMP are not
implemented, so DNS over UDP, `ping`, and applications that need raw IP do not
work through the proxy. The SOCKS5 listener has no authentication and binds
loopback by default; any local process can use it.

TUN mode has the following limits.

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

Four packages, one job each. `overlay` runs the node, `proxy` turns a peer
connection into a SOCKS5 listener, `transport` carries QUIC and certificates,
`relay` serves a splice of your own. `go get` on its own
starts nothing: your program owns the process, the certificates, and the
pairing, so it does the work that `ens` does around the node.

Set `overlay.Config.OnPeer` to receive the authenticated peer connection
instead of opening a TUN. `proxy.Server` wraps that connection in yamux and
serves SOCKS5. `cmd/ens/main.go` shows the wiring for both modes.

```bash
go get github.com/fdaio/enserie@main
```

The proxy API is on `main`; the v0.2.18 module does not include it.

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
make dist-linux VERSION=0.2.18
make dist-darwin VERSION=0.2.18
```

## License

Apache-2.0 — see [LICENSE](LICENSE).

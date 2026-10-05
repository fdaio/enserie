# enserie protocol

How two `ens` nodes find each other, trust each other, and carry IPv4 packets.
This describes what the code does today, including the parts that are known to
be rough.

## Roles

One node **offers** and one **accepts**. The offering node listens and never
dials; the accepting node dials. `overlay.Node` enforces this: with
`Config.Invite` set, the dial loop returns immediately.

The reason is in `overlay/hub.go`. Two relay dials that arrive together pick
opposite splices, and each side then closes the connection the other one is
using. Only one dialer avoids that.

## Pairing

The offering node prints a one-line command holding a token. That token is the
whole pairing exchange.

```
machine A                          machine Z
ens invite
  picks a /30 link          ──────▶  ens accept TOKEN
  writes an ephemeral cert
  offers to each relay
  waits for a ticket              ──▶  dials the candidates, then each relay
  accepts the ticket
  both nodes now have a path
```

### The token

`overlay.Invite`, base64 of JSON, seven fields:

| field | meaning |
|---|---|
| `v` | format version, currently `1` |
| `exp` | expiry, Unix seconds |
| `id` | the offering node id |
| `fp` | SHA-256 fingerprint of the offering node certificate, hex |
| `cidr` | the offering node overlay address, for example `10.7.0.1/30` |
| `addrs` | QUIC candidates the offering node can be dialed on |
| `relays` | relay URLs both nodes use |
| `s` | the shared secret |

The token is **base64, not encryption**. Anyone who reads it holds the secret
and can impersonate the accepting node, so treat it like a password. See
[SECURITY.md](SECURITY.md) for what it does not yet do, such as expiry.

### What each side checks

The two sides cannot both pin a fingerprint, because the offering node has no
way to know the accepting node's certificate before the connection exists.

The accepting node pins the inviter by the fingerprint in the token, over a
TLS 1.3 connection (`transport.DialQUIC`, or `transport.ClientE2E` across a
relay).

The offering node cannot pin anything, so `overlay.Node.allowFP` accepts any
non-empty fingerprint. It authenticates the peer with the shared secret in the
first frame instead. The accepting node proves it holds the secret, and the
offering node binds the peer id from that same frame.

### Expiry and single use

A token carries `exp`, and `Encode` fills it with now plus
`overlay.InviteTTL` (30 minutes). `ParseInvite` refuses a token past that
deadline, and also refuses one with no deadline at all, because a credential of
unknown age cannot be aged out.

The offering node is what enforces single use. `overlay.Node` records the peer
it accepted and marks itself paired once a path is up, and it then refuses a
different peer id. Without that, a second holder of the same token could pair
under another name, since the bound peer id is the only thing telling two peers
apart. A reconnect from the same peer id is still accepted.

Note the limit: an expiry stops a stale token being used later, but it does not
stop a token being replayed inside its window. Only the offering node can
enforce single use, because only it sees the second attempt.

### Certificates

`transport.EnsureServerCert` creates a self-signed certificate when the files
are missing. The CLI writes it under a fresh `os.MkdirTemp` directory, so a
node's certificate is **ephemeral and never persists across a restart**. Peers
pin certificates by fingerprint rather than by a certificate authority, so the
names inside a certificate are never checked.

Because the certificate is regenerated on every start, a node that reconnects
presents a new fingerprint. Any token issued before the restart refers to the
old one. Certificates are never rotated on a schedule and cannot be revoked.

## Paths

QUIC is tried first, the relay is the fallback.

### Direct QUIC

The offering node binds `0.0.0.0:0` and passes its candidates in the token.
`transport.ExpandCandidates` enumerates the local interfaces, and
`transport.PreferNonLoopback` puts loopback last, because loopback only helps
on one host.

The accepting node dials the candidates in order and each miss waits out a
15 second dial timeout, so the order decides how long a bad address costs. A
public address is tried first, then a LAN address, then `100.64.0.0/10`, the
range carriers hand out and the range Tailscale uses, since an address there
answers only when the peer is on the same VPN. Nothing is dropped, because a
LAN address is how two machines on one network pair directly. `ens invite
--advertise HOST` puts a named address ahead of all of them, which is the way
out when the only reachable address is a port mapping or a resolvable name.

There is no STUN and no address discovery, so a node behind a symmetric NAT
contributes no usable candidate. Candidates come from what the operating system
already knows.

### Relay

Each node holds a WebSocket to the relay. `relay.Offer` registers a rendezvous
for the node id and calls back with a ticket when a peer dials.
`relay.Dial` asks for a peer by id, and `relay.Accept` claims a ticket.

The relay is a blind pipe. `overlay.Node` wraps each leg in TLS 1.3 right after
the splice, so the relay copies ciphertext: it never sees an IP packet. What it
can see is the metadata of the connection, which is the two IP addresses, when
the connection was made, how many bytes moved, and how long it lasted.

The relay protocol is length-prefixed JSON, `relay.Msg`, with six message
types: `offer`, `dial`, `accept`, `incoming`, `ok`, `error`. A control message
is capped at 64 KiB. After the handshake, the connection is a raw pipe.

### The order the offer must keep

The offering node sends its ack **before** it publishes the offer, and the ack
goes out under the same lock as the ticket write. A dial that arrives earlier
would deliver its ticket first, and the client reads the ack as the first
message and tears the offer down on anything else, discarding the ticket. The
dial would then wait out its full accept window for a peer that never heard of
it. `relay.TestOfferIsPublishedOnlyAfterItsAck` guards this.

### A path does not change

`overlay.Node.installPeer` keeps the first peer it accepts and drops any later
connection, logging `peer already connected`. A link that starts on the relay
stays there for the life of the node even after direct QUIC becomes possible.
Migrating would mean holding two connections and choosing between them, which
the current single `peerConn` cannot express.

## The data plane

An IP packet becomes one frame: a type byte, a length-prefixed JSON-free body.
`typePacket` carries a packet, `typeHello` carries the node id and the secret.
The cap is 64 KiB.

The interface MTU is fixed at 1280 so that a packet fits inside the smallest
plausible underlay MTU without fragmenting.

### Streams, not datagrams

Overlay packets travel in a single ordered QUIC stream. `transport.ListenQUIC`
accepts with `AcceptStream` and `transport.DialQUIC` opens with
`OpenStreamSync`, so both directions end up on one stream per connection. One
lost segment holds up everything behind it, and the inner IP layer and QUIC both
run congestion control. QUIC datagrams would remove both effects.

## Trust boundaries

| party | can trust | cannot trust |
|---|---|---|
| the peer | the packets it receives come from the fingerprint it pinned | anything the relay said |
| the relay | it copies bytes without reading them | it cannot see packet contents, but it sees connection metadata and can drop or delay |
| the token reader | nothing | the secret, so they can impersonate the accepting node |
| a network observer | nothing | packets are inside TLS 1.3, but sizes and timing are visible |

There is no account, no control plane, and no key distribution. The token is
the whole thing, and both sides are online at the same moment.

## What the code does not do yet

- A token can still be replayed inside its expiry window by a peer that has not
  paired yet.
- Certificates are neither rotated nor revocable.
- Only IPv4, and only a `/30` for two addresses.
- `overlay.Node` holds exactly one peer. Multi-peer needs a peer set, and
  `Config.Peer`, `Path()` and `peerConn` are all singular today.
- No STUN, no simultaneous hole punching, no relay-to-direct migration.
- No packet loss or throughput figures exist for either path.
- Linux and macOS are the only platforms. `overlay/tun_other.go` is a stub that
  fails on purpose, so a build for another operating system compiles and then
  refuses to open a TUN instead of pretending to work.

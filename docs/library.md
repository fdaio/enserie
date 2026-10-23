# Using enserie as a library

`overlay` runs the node, `proxy` turns a peer connection into a SOCKS5 listener,
`transport` carries QUIC and certificates, and `relay` serves a splice of your
own. The [README](../README.md) covers the short version. This page shows the
complete pairing setup.

## Pairing

Pairing is the same out-of-band token exchange that `ens invite` prints. The
inviting node only listens and offers, so it must not dial. The accepting node
dials, even when its own id sorts first.

The snippets skip the errors that the generators cannot fail on. Check the rest.
They assume the imports and context used by `cmd/ens/main.go`.

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

See [What the token carries](../README.md#what-the-token-carries) for the token
fields. [`cmd/ens/main.go`](../cmd/ens/main.go) shows the full wiring for both
modes.

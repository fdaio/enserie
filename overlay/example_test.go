package overlay_test

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"

	"github.com/fdaio/enserie/overlay"
)

// Example shows the exchange an importing program must complete before the
// overlay carries packets: one prefix and one token. It stops before Start,
// because a TUN needs root.
func Example() {
	const inviteCIDR = "10.7.0.1/30"
	const acceptCIDR = "10.7.0.2/30"

	// The inviting node reserves the other half of its prefix for the peer.
	other, err := overlay.OtherCIDR(inviteCIDR)
	if err != nil {
		log.Fatal(err)
	}
	peerIP, _, err := net.ParseCIDR(other)
	if err != nil {
		log.Fatal(err)
	}

	// The token carries everything the accepting node cannot guess. Relays are
	// left out on purpose, so ParseInvite fills in the hosted splices.
	token, err := overlay.Invite{
		V:      1,
		ID:     "node-a",
		FP:     "aabbccddeeff",
		CIDR:   inviteCIDR,
		Addrs:  []string{"198.51.100.10:4433"},
		Secret: "s3cret",
	}.Encode()
	if err != nil {
		log.Fatal(err)
	}

	inv, err := overlay.ParseInvite(token)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("relays", inv.Relays)

	// The inviting node listens and offers, so it must not dial.
	if _, err := overlay.New(overlay.Config{
		ID:     inv.ID,
		CIDR:   inviteCIDR,
		Cert:   tls.Certificate{},
		Peer:   overlay.Peer{IP: peerIP},
		Secret: inv.Secret,
		Invite: true,
	}); err != nil {
		log.Fatal(err)
	}

	// The accepting node takes the other half and dials, even though its id
	// sorts first, because two relay dials tear down each other's splice.
	localIP, _, err := net.ParseCIDR(inviteCIDR)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := overlay.New(overlay.Config{
		ID:         "node-z",
		CIDR:       acceptCIDR,
		Cert:       tls.Certificate{},
		Peer:       overlay.Peer{ID: inv.ID, IP: localIP, CertFP: inv.FP, Candidates: inv.Addrs},
		Secret:     inv.Secret,
		AlwaysDial: true,
	}); err != nil {
		log.Fatal(err)
	}

	// A peer outside the prefix is refused instead of silently forwarded.
	_, err = overlay.New(overlay.Config{
		ID:         "node-z",
		CIDR:       acceptCIDR,
		Peer:       overlay.Peer{ID: inv.ID, IP: net.ParseIP("192.0.2.9"), CertFP: inv.FP},
		AlwaysDial: true,
	})
	fmt.Println("outside prefix:", err)

	// Output:
	// relays [https://relay-1.getfda.dev https://relay-2.getfda.dev]
	// outside prefix: peer IP 192.0.2.9 is outside 10.7.0.0/30
}

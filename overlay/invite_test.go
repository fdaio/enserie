package overlay

import (
	"strings"
	"testing"
)

func TestInviteTokenRoundTrip(t *testing.T) {
	inv := Invite{
		V:      1,
		ID:     "abc",
		FP:     "deadbeef",
		CIDR:   "10.7.0.1/30",
		Addrs:  []string{"192.0.2.1:4242"},
		Relays: DefaultRelayURLs(),
		Secret: "s3cret",
	}
	tok, err := inv.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(tok, "+/=") {
		t.Fatalf("token is not url-safe: %q", tok)
	}
	got, err := ParseInvite(tok)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != inv.ID || got.FP != inv.FP || got.CIDR != inv.CIDR || got.Secret != inv.Secret {
		t.Fatalf("round trip %#v", got)
	}
	if len(got.Relays) != 2 {
		t.Fatalf("relays %v", got.Relays)
	}
}

func TestRandomLink(t *testing.T) {
	cidr, err := RandomLink()
	if err != nil {
		t.Fatal(err)
	}
	local, n, err := parseIPv4(cidr)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := OtherCIDR(cidr)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := parseIPv4(peer)
	if err != nil {
		t.Fatal(err)
	}
	if !n.Contains(local) || !n.Contains(other) || local.Equal(other) {
		t.Fatalf("bad link %s peer %s", cidr, peer)
	}
	if local[0] != 198 || (local[1] != 18 && local[1] != 19) {
		t.Fatalf("overlay %s is not in 198.18.0.0/15", cidr)
	}
}

func TestOtherCIDR(t *testing.T) {
	got, err := OtherCIDR("10.7.0.1/30")
	if err != nil {
		t.Fatal(err)
	}
	if got != "10.7.0.2/30" {
		t.Fatalf("got %s", got)
	}
	back, err := OtherCIDR(got)
	if err != nil {
		t.Fatal(err)
	}
	if back != "10.7.0.1/30" {
		t.Fatalf("back %s", back)
	}
}

func TestFilterOverlayAddrs(t *testing.T) {
	_, n, err := parseIPv4("10.7.0.1/30")
	if err != nil {
		t.Fatal(err)
	}
	got := FilterOverlayAddrs([]string{"10.7.0.1:4242", "192.0.2.8:9", "127.0.0.1:1"}, n)
	if len(got) != 2 || got[0] != "192.0.2.8:9" || got[1] != "127.0.0.1:1" {
		t.Fatalf("got %v", got)
	}
}

func TestDefaultRelayURLs(t *testing.T) {
	got := DefaultRelayURLs()
	if len(got) != 2 {
		t.Fatalf("got %d", len(got))
	}
	for _, u := range got {
		if !strings.HasPrefix(u, "https://relay-") || !strings.HasSuffix(u, ".getfda.dev") {
			t.Errorf("unexpected hosted relay %q", u)
		}
	}
}

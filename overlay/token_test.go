package overlay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fdaio/enserie/transport"
)

func tokenFromInvite(t *testing.T, inv Invite) string {
	t.Helper()
	token, err := inv.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return token
}

func TestInviteCarriesAnExpiry(t *testing.T) {
	inv := Invite{ID: "a", FP: "aa", CIDR: "198.18.0.1/30", Secret: "s3cret"}
	token := tokenFromInvite(t, inv)

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	exp, ok := fields["exp"].(float64)
	if !ok {
		t.Fatal("token has no exp field")
	}
	deadline := time.Unix(int64(exp), 0)
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > InviteTTL {
		t.Errorf("expiry is %s away, want between 0 and %s", remaining, InviteTTL)
	}
}

func TestParseInviteRejectsAnExpiredToken(t *testing.T) {
	inv := Invite{
		ID:     "a",
		FP:     "aa",
		CIDR:   "198.18.0.1/30",
		Secret: "s3cret",
		Exp:    time.Now().Add(-time.Minute).Unix(),
	}
	_, err := ParseInvite(tokenFromInvite(t, inv))
	if err == nil {
		t.Fatal("ParseInvite accepted an expired token")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error = %v, want it to say the token expired", err)
	}
}

func TestParseInviteRejectsATokenWithoutAnExpiry(t *testing.T) {
	// A token with no deadline cannot be aged out, so it must not be taken.
	var fields = map[string]any{
		"v":    inviteVersion,
		"id":   "a",
		"fp":   "aa",
		"cidr": "198.18.0.1/30",
		"s":    "s3cret",
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if _, err := ParseInvite(token); err == nil {
		t.Fatal("ParseInvite accepted a token with no expiry")
	} else if !strings.Contains(err.Error(), "no expiry") {
		t.Errorf("error = %v, want it to mention the missing expiry", err)
	}
}

func TestParseInviteAcceptsAFreshToken(t *testing.T) {
	inv := Invite{ID: "a", FP: "aa", CIDR: "198.18.0.1/30", Secret: "s3cret"}
	got, err := ParseInvite(tokenFromInvite(t, inv))
	if err != nil {
		t.Fatalf("ParseInvite: %v", err)
	}
	if got.ID != "a" || got.Secret != "s3cret" {
		t.Errorf("round trip lost data: %+v", got)
	}
}

func TestParseInviteKeepsACallerSuppliedDeadline(t *testing.T) {
	// A caller that needs more time than the default, such as a script that
	// waits for a human, must be able to ask for it.
	deadline := time.Now().Add(2 * InviteTTL).Unix()
	inv := Invite{ID: "a", FP: "aa", CIDR: "198.18.0.1/30", Secret: "s", Exp: deadline}
	got, err := ParseInvite(tokenFromInvite(t, inv))
	if err != nil {
		t.Fatalf("ParseInvite: %v", err)
	}
	if got.Exp != deadline {
		t.Errorf("Exp = %d, want %d", got.Exp, deadline)
	}
}

// An invite node that has taken a peer refuses a different peer id. The state
// comes from a real handshake, so removing the pairing from serveConn fails
// here.
func TestPairedInviteNodeRefusesAnotherPeerID(t *testing.T) {
	inviter, acceptor := pairInviteNode(t, "node-z")
	if !inviter.paired {
		t.Fatal("the inviter did not record a pairing after the handshake")
	}
	if inviter.bindPeerID("node-z") != true {
		t.Error("the same peer id was refused after pairing, which would break a reconnect")
	}
	if inviter.bindPeerID("node-y") {
		t.Error("an invite node that already paired accepted a different peer id")
	}
	_ = acceptor
}

// invitePair brings up an invite node and an accepting node over a real
// connection, the way ens invite and ens accept do.
func pairInviteNode(t *testing.T, acceptorID string) (*Node, *Node) {
	t.Helper()
	dir := t.TempDir()
	certA, err := transport.EnsureServerCert(filepath.Join(dir, "a.crt"), filepath.Join(dir, "a.key"))
	if err != nil {
		t.Fatal(err)
	}
	certZ, err := transport.EnsureServerCert(filepath.Join(dir, "z.crt"), filepath.Join(dir, "z.key"))
	if err != nil {
		t.Fatal(err)
	}
	fpA, err := transport.CertFingerprint(certA)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	inviter, err := New(Config{
		ID:     "node-a",
		CIDR:   "198.18.0.1/30",
		Cert:   certA,
		Listen: "127.0.0.1:0",
		Secret: "s3cret",
		Invite: true,
		Peer:   Peer{IP: net.ParseIP("198.18.0.2")},
		Device: newMemDevice(),
	})
	if err != nil {
		t.Fatalf("New inviter: %v", err)
	}
	if err := inviter.Start(ctx); err != nil {
		t.Fatalf("start inviter: %v", err)
	}
	t.Cleanup(func() { _ = inviter.Close() })

	acceptor, err := New(Config{
		ID:         acceptorID,
		CIDR:       "198.18.0.2/30",
		Cert:       certZ,
		Listen:     "127.0.0.1:0",
		Secret:     "s3cret",
		AlwaysDial: true,
		Peer: Peer{
			ID:         "node-a",
			IP:         net.ParseIP("198.18.0.1"),
			CertFP:     fpA,
			Candidates: []string{inviter.ListenAddr()},
		},
		Device: newMemDevice(),
	})
	if err != nil {
		t.Fatalf("New acceptor: %v", err)
	}
	if err := acceptor.Start(ctx); err != nil {
		t.Fatalf("start acceptor: %v", err)
	}
	t.Cleanup(func() { _ = acceptor.Close() })

	waitPath(t, inviter, acceptor, transport.KindQUIC)
	return inviter, acceptor
}

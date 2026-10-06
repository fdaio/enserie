package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/fdaio/enserie/overlay"
)

func TestInviteCLITokenIsShellSafe(t *testing.T) {
	inv := overlay.Invite{
		V:      1,
		ID:     "a",
		FP:     strings.Repeat("ab", 32),
		CIDR:   "10.7.0.1/30",
		Secret: "s",
		Relays: overlay.DefaultRelayURLs(),
	}
	tok, err := inv.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(tok, " \n'\"") {
		t.Fatalf("token has shell metacharacters: %q", tok)
	}
	if _, err := overlay.ParseInvite(tok); err != nil {
		t.Fatal(err)
	}
}

func TestWrapStartOmitsNeedRootWhenRoot(t *testing.T) {
	err := wrapStart(fmt.Errorf("TUNSETIFF: device or resource busy"))
	s := err.Error()
	if os.Geteuid() == 0 && strings.Contains(s, "need root") {
		t.Fatalf("root must not see need root: %s", s)
	}
	if os.Geteuid() != 0 && !strings.Contains(s, "need root") {
		t.Fatalf("non-root should see need root: %s", s)
	}
}

// Every run used to leave one ens-XXXXXXXX directory holding a private key in
// the temporary directory, because nothing removed it.
func TestEphemeralCertLeavesNothingBehind(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	cert, err := ephemeralCert()
	if err != nil {
		t.Fatalf("ephemeralCert: %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("no certificate returned")
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("ephemeralCert left %d entries behind: %v", len(entries), names)
	}
}

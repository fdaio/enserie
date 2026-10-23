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
	err := wrapStart(fmt.Errorf("TUNSETIFF: device or resource busy"), overlay.ModeTun)
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

// Proxy mode runs as the reader's own user, so the peer must not be told to
// use sudo.
func TestAcceptCommandProxyNeedsNoSudo(t *testing.T) {
	got := acceptCommand("TOKEN", overlay.ModeProxy)
	if got != "ens accept TOKEN" {
		t.Fatalf("acceptCommand = %q, want %q", got, "ens accept TOKEN")
	}
}

// A proxy start failure must not send the reader after root. The usual cause
// is a busy port or a bad relay, not a TUN permission.
func TestWrapStartProxyOmitsNeedRoot(t *testing.T) {
	err := wrapStart(fmt.Errorf("socks listen 127.0.0.1:1080: address already in use"), overlay.ModeProxy)
	if strings.Contains(err.Error(), "need root") || strings.Contains(err.Error(), "sudo") {
		t.Fatalf("proxy error mentions root: %s", err)
	}
	if !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("proxy error lost the cause: %s", err)
	}
}

// The command the peer runs must not name a path. The peer may have installed
// with a different package manager, at a different prefix, or not at all, and
// this machine's path is wrong on every one of those.
func TestAcceptCommandNamesNoPath(t *testing.T) {
	got := acceptCommand("TOKEN", overlay.ModeTun)
	if got != "sudo ens accept TOKEN" {
		t.Fatalf("acceptCommand = %q, want %q", got, "sudo ens accept TOKEN")
	}
	if strings.ContainsAny(got, "/\\") {
		t.Fatalf("acceptCommand names a path: %q", got)
	}
	if self := selfPath(); self != "" && strings.Contains(got, self) {
		t.Fatalf("acceptCommand carries this machine's path %q: %q", self, got)
	}
}

// The commands a reader runs here do need the path, because sudo drops PATH.
func TestSudoCommandIsForThisMachine(t *testing.T) {
	got := sudoCommand("down")
	if !strings.HasPrefix(got, "sudo ") {
		t.Fatalf("sudoCommand = %q, want a sudo command", got)
	}
	if self := selfPath(); self != "" && !strings.Contains(got, self) {
		t.Fatalf("sudoCommand = %q, want it to name %q", got, self)
	}
}

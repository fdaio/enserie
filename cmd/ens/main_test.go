package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fdaio/enserie/overlay"
	"github.com/fdaio/enserie/transport"
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

func TestReportPathPrintsConnectionOnly(t *testing.T) {
	var out bytes.Buffer
	connected := reportPathTo(context.Background(), func() transport.Kind {
		return transport.KindRelay
	}, &out, time.Hour, time.Millisecond)
	if !connected {
		t.Fatal("reportPathTo did not report a connection")
	}
	if got, want := out.String(), "ens: connected via relay\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestReportPathPrintsWaitBeforeConnection(t *testing.T) {
	var calls atomic.Int32
	var out bytes.Buffer
	connected := reportPathTo(context.Background(), func() transport.Kind {
		if calls.Add(1) == 1 {
			return ""
		}
		return transport.KindRelay
	}, &out, time.Hour, time.Millisecond)
	if !connected {
		t.Fatal("reportPathTo did not report a connection")
	}
	got := out.String()
	if !strings.Contains(got, "ens: waiting for peer (Ctrl-C to cancel)\n") {
		t.Errorf("output has no waiting line: %q", got)
	}
	if !strings.HasSuffix(got, "ens: connected via relay\n") {
		t.Errorf("output has no connection line: %q", got)
	}
}

func TestReportPathPrintsProgressOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out lockedBuffer
	done := make(chan bool, 1)
	go func() {
		done <- reportPathTo(ctx, func() transport.Kind { return "" }, &out, 10*time.Millisecond, time.Millisecond)
	}()
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(out.String(), "still waiting for peer") {
		if time.Now().After(deadline) {
			t.Fatalf("no progress line; output %q", out.String())
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if connected := <-done; connected {
		t.Fatal("reportPathTo reported a connection after cancellation")
	}
	got := out.String()
	if n := strings.Count(got, "ens: waiting for peer (Ctrl-C to cancel)\n"); n != 1 {
		t.Errorf("waiting line count = %d, want 1: %q", n, got)
	}
	if n := strings.Count(got, "ens: still waiting for peer (10ms)\n"); n != 1 {
		t.Errorf("progress line count = %d, want 1: %q", n, got)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
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

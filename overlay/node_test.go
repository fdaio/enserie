package overlay

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/fdaio/enserie/relay"
	"github.com/fdaio/enserie/transport"
)

func TestOverlayQUICForwardsPacket(t *testing.T) {
	a, b := pair(t, false)
	defer a.Close()
	defer b.Close()
	waitPath(t, a, b, transport.KindQUIC)
	sendAndRecv(t, a, b)
}

func TestOverlayRelayForwardsPacket(t *testing.T) {
	a, b := pair(t, true)
	defer a.Close()
	defer b.Close()
	waitPath(t, a, b, transport.KindRelay)
	sendAndRecv(t, a, b)
}

func TestOverlayInviteAcceptQUIC(t *testing.T) {
	a, z := invitePair(t, false)
	defer a.Close()
	defer z.Close()
	waitPath(t, a, z, transport.KindQUIC)
	sendAndRecv(t, a, z)
}

func TestOverlayInviteAcceptRelay(t *testing.T) {
	a, z := invitePair(t, true)
	defer a.Close()
	defer z.Close()
	waitPath(t, a, z, transport.KindRelay)
	sendAndRecv(t, a, z)
}

func invitePair(t *testing.T, forceRelay bool) (*Node, *Node) {
	t.Helper()
	dir := t.TempDir()
	ca, err := transport.EnsureServerCert(filepath.Join(dir, "a.crt"), filepath.Join(dir, "a.key"))
	if err != nil {
		t.Fatal(err)
	}
	cz, err := transport.EnsureServerCert(filepath.Join(dir, "z.crt"), filepath.Join(dir, "z.key"))
	if err != nil {
		t.Fatal(err)
	}
	fpA, err := transport.CertFingerprint(ca)
	if err != nil {
		t.Fatal(err)
	}
	var relays []string
	if forceRelay {
		addr, closeFn, err := relay.ListenAndServe("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = closeFn() })
		url := "http://" + addr.String()
		waitHTTP(t, url+"/healthz")
		relays = []string{url}
	}
	secret := "invite-secret"
	ctx := context.Background()
	a, err := New(Config{
		ID:     "a",
		CIDR:   "10.7.0.1/30",
		Cert:   ca,
		Listen: "127.0.0.1:0",
		Relays: relays,
		Peer: Peer{
			IP: net.ParseIP("10.7.0.2"),
		},
		Device: newMemDevice(),
		Secret: secret,
		Invite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if forceRelay {
		time.Sleep(200 * time.Millisecond)
	}
	zPeer := Peer{
		ID:         "a",
		IP:         net.ParseIP("10.7.0.1"),
		CertFP:     fpA,
		ForceRelay: forceRelay,
	}
	if !forceRelay {
		zPeer.Candidates = []string{a.ListenAddr()}
	}
	z, err := New(Config{
		ID:         "z",
		CIDR:       "10.7.0.2/30",
		Cert:       cz,
		Listen:     "127.0.0.1:0",
		Relays:     relays,
		Peer:       zPeer,
		Device:     newMemDevice(),
		Secret:     secret,
		AlwaysDial: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := z.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return a, z
}

func TestOverlayRelayOffFailsWithoutCandidates(t *testing.T) {
	dir := t.TempDir()
	ca, err := transport.EnsureServerCert(filepath.Join(dir, "a.crt"), filepath.Join(dir, "a.key"))
	if err != nil {
		t.Fatal(err)
	}
	cb, err := transport.EnsureServerCert(filepath.Join(dir, "b.crt"), filepath.Join(dir, "b.key"))
	if err != nil {
		t.Fatal(err)
	}
	fpB, err := transport.CertFingerprint(cb)
	if err != nil {
		t.Fatal(err)
	}
	n, err := New(Config{
		ID:     "a",
		CIDR:   "10.7.0.1/30",
		Cert:   ca,
		Listen: "127.0.0.1:0",
		Peer: Peer{
			ID:     "z",
			IP:     net.ParseIP("10.7.0.2"),
			CertFP: fpB,
		},
		Device: newMemDevice(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = n.dialPeer(context.Background())
	if err == nil {
		t.Fatal("expected dial failure with no candidates and no relay")
	}
}

func pair(t *testing.T, forceRelay bool) (*Node, *Node) {
	t.Helper()
	dir := t.TempDir()
	ca, err := transport.EnsureServerCert(filepath.Join(dir, "a.crt"), filepath.Join(dir, "a.key"))
	if err != nil {
		t.Fatal(err)
	}
	cb, err := transport.EnsureServerCert(filepath.Join(dir, "b.crt"), filepath.Join(dir, "b.key"))
	if err != nil {
		t.Fatal(err)
	}
	fpA, err := transport.CertFingerprint(ca)
	if err != nil {
		t.Fatal(err)
	}
	fpB, err := transport.CertFingerprint(cb)
	if err != nil {
		t.Fatal(err)
	}

	var relays []string
	if forceRelay {
		addr, closeFn, err := relay.ListenAndServe("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = closeFn() })
		url := "http://" + addr.String()
		waitHTTP(t, url+"/healthz")
		relays = []string{url}
	}

	ctx := context.Background()
	a, err := New(Config{
		ID:     "a",
		CIDR:   "10.7.0.1/30",
		Cert:   ca,
		Listen: "127.0.0.1:0",
		Relays: relays,
		Peer: Peer{
			ID:         "z",
			IP:         net.ParseIP("10.7.0.2"),
			CertFP:     fpB,
			ForceRelay: forceRelay,
		},
		Device: newMemDevice(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if forceRelay {
		time.Sleep(200 * time.Millisecond)
	}
	bPeer := Peer{
		ID:         "a",
		IP:         net.ParseIP("10.7.0.1"),
		CertFP:     fpA,
		ForceRelay: forceRelay,
	}
	if !forceRelay {
		bPeer.Candidates = []string{a.ListenAddr()}
	}
	b, err := New(Config{
		ID:     "z",
		CIDR:   "10.7.0.2/30",
		Cert:   cb,
		Listen: "127.0.0.1:0",
		Relays: relays,
		Peer:   bPeer,
		Device: newMemDevice(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return a, b
}

func waitPath(t *testing.T, a, b *Node, want transport.Kind) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if a.Path() == want && b.Path() == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("path a=%q b=%q want %q", a.Path(), b.Path(), want)
}

func sendAndRecv(t *testing.T, a, b *Node) {
	t.Helper()
	src := net.ParseIP("10.7.0.1").To4()
	dst := net.ParseIP("10.7.0.2").To4()
	pkt := ipv4Packet(src, dst, []byte("ping-overlay"))
	deadline := time.Now().Add(20 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		if err := a.Inject(pkt); err != nil {
			last = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		got, err := b.WaitPacket(500 * time.Millisecond)
		if err != nil {
			last = err
			continue
		}
		if !bytesEqual(got, pkt) {
			t.Fatalf("payload mismatch: %q vs %q", got, pkt)
		}
		return
	}
	t.Fatalf("packet did not arrive: %v", last)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func waitHTTP(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("relay not ready at %s", url)
}

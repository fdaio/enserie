package overlay

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/fdaio/enserie/relay"
	"github.com/fdaio/enserie/transport"
)

// blackholeAddr is routable and answers nothing, which is what the LAN address
// in someone else's invite looks like from a different network.
const blackholeAddr = "192.0.2.1:49162"

// A peer whose advertised addresses cannot be answered from here must still be
// reachable through the relay, and quickly. The addresses used to be tried one
// after another and each was allowed its full handshake timeout, so a peer
// behind a NAT cost several seconds of dropped packets before the relay was
// even tried.
func TestDialPeerReachesTheRelayWithoutWaitingForTheAddresses(t *testing.T) {
	relayAddr, closeRelay, err := relay.ListenAndServe("127.0.0.1:0")
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	t.Cleanup(func() { _ = closeRelay() })
	relayURL := "http://" + relayAddr.String()
	waitHTTP(t, relayURL+"/healthz")

	cert, err := transport.EnsureServerCert(t.TempDir()+"/tls.crt", t.TempDir()+"/tls.key")
	if err != nil {
		t.Fatalf("cert: %v", err)
	}
	fp, err := transport.CertFingerprint(cert)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}

	// The far end offers on the relay, which is the side that has to answer.
	inviter, err := New(Config{
		ID:     "a",
		CIDR:   "10.7.0.1/30",
		Cert:   cert,
		Listen: "127.0.0.1:0",
		Relays: []string{relayURL},
		Peer:   Peer{ID: "z", IP: net.ParseIP("10.7.0.2"), CertFP: fp},
		Invite: true,
		Secret: "dial-peer-test",
		Device: newMemDevice(),
	})
	if err != nil {
		t.Fatalf("inviter: %v", err)
	}
	if err := inviter.Start(context.Background()); err != nil {
		t.Fatalf("inviter start: %v", err)
	}
	t.Cleanup(func() { _ = inviter.Close() })

	// The dialler cannot answer two blackhole addresses, and the loopback entry
	// points at its own machine where nothing is listening.
	dialer, err := New(Config{
		ID:     "z",
		CIDR:   "10.7.0.2/30",
		Cert:   cert,
		Listen: "127.0.0.1:0",
		Relays: []string{relayURL},
		Secret: "dial-peer-test",
		Peer: Peer{
			ID:         "a",
			IP:         net.ParseIP("10.7.0.1"),
			CertFP:     fp,
			Candidates: []string{blackholeAddr, "192.0.2.2:49162", "127.0.0.1:1"},
		},
		Device: newMemDevice(),
	})
	if err != nil {
		t.Fatalf("dialer: %v", err)
	}

	addrs, relays := dialTargets(dialer.cfg)
	if len(addrs) != 3 {
		t.Fatalf("got %d addresses %v, the test needs addresses that cannot answer", len(addrs), addrs)
	}
	if len(relays) != 1 {
		t.Fatalf("got relays %v", relays)
	}

	start := time.Now()
	conn, kind, err := dialer.dialPeer(context.Background())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("dialPeer: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if kind != transport.KindRelay {
		t.Errorf("got kind %q, want the relay", kind)
	}

	// The connection has to survive dialPeer returning. relay.Dial closes it
	// when the context it was opened under is done, and the race cancels its own
	// context the moment a path is chosen, so a connection opened under the dial
	// context would come back already closed.
	if err := probeRead(conn); err != nil {
		t.Errorf("the connection came back closed, read returned %v", err)
	}
	// Sequentially the three addresses would each have cost a handshake
	// timeout, which measures at about five seconds each. Racing them leaves
	// only the hedge before the relay starts.
	if elapsed > 2*time.Second {
		t.Errorf("dialPeer took %v, so the relay still waited for the addresses", elapsed)
	}
}

// probeRead reports whether a read on the connection fails straight away. A
// live path has nothing to read and blocks, so a quick error means the
// connection was already shut. The buffer is larger than any single frame the
// relay would send unasked, so a live connection cannot satisfy the read.
func probeRead(c net.Conn) error {
	ch := make(chan error, 1)
	go func() {
		buf := make([]byte, 512)
		_, err := c.Read(buf)
		ch <- err
	}()
	select {
	case err := <-ch:
		return err
	case <-time.After(300 * time.Millisecond):
		return nil
	}
}

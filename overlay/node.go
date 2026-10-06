// Package overlay runs one end of a two-node overlay link.
//
// Two nodes take the two addresses of an IPv4 /30 and reach each other as if
// they were on a LAN. New checks the pairing. Start opens the TUN, listens,
// offers to the relays, and dials the peer. Close stops the node.
//
// A program pairs the two nodes itself. The token in this package is the same
// token that `ens invite` prints.
package overlay

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/fdaio/enserie/relay"
	"github.com/fdaio/enserie/transport"
)

// Peer is the other end of a /30 (or larger) overlay link.
type Peer struct {
	// ID is the peer node id. An invite node learns it from the first hello
	// and leaves it empty.
	ID string
	// IP is the peer overlay address. It must sit in the same prefix as
	// Config.CIDR and differ from the local address.
	IP net.IP
	// CertFP is the peer certificate fingerprint in hex. An invite node
	// cannot know it beforehand, so it accepts any non-empty one.
	CertFP string
	// Candidates are dial targets, tried in the given order.
	Candidates []string
	// ForceRelay skips QUIC and dials a relay instead.
	ForceRelay bool
}

// Config describes one node and the peer it must reach.
type Config struct {
	// ID is this node id. It must differ from Peer.ID.
	ID string
	// CIDR is the local overlay address, e.g. 10.7.0.1/30.
	CIDR string
	// Cert is this node certificate. The peer pins it by fingerprint.
	Cert tls.Certificate
	// Listen is the QUIC listen address, default 0.0.0.0:0.
	Listen string
	// Relays are the relay URLs to offer to and to dial. An empty entry or
	// "off" is skipped, and an empty list leaves QUIC as the only path.
	Relays []string
	// Peer describes the other end.
	Peer Peer
	// Device is the packet source and sink. Nil opens a real TUN.
	Device Device
	// Secret is the shared invite secret; empty skips the check.
	Secret string
	// Invite listens and offers. It does not dial. The first hello with
	// a matching secret becomes the peer.
	Invite bool
	// AlwaysDial makes this node the dialer even when its id is smaller.
	AlwaysDial bool
}

// Node is one overlay node. Create it with New and run it with Start.
type Node struct {
	cfg      Config
	local    net.IP
	prefix   *net.IPNet
	dev      Device
	mu       sync.Mutex
	peerConn net.Conn
	peerKind transport.Kind
	paired   bool
	ln       net.Listener
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// New checks the pairing and returns a node that is not running yet.
func New(cfg Config) (*Node, error) {
	if cfg.ID == "" {
		return nil, fmt.Errorf("node id required")
	}
	if cfg.Invite && cfg.AlwaysDial {
		return nil, fmt.Errorf("invite node does not dial")
	}
	if !cfg.Invite {
		if cfg.Peer.ID == "" || cfg.Peer.ID == cfg.ID {
			return nil, fmt.Errorf("peer id required and must differ")
		}
		if transport.NormalizeFP(cfg.Peer.CertFP) == "" {
			return nil, fmt.Errorf("peer certificate fingerprint required")
		}
	} else if cfg.Secret == "" {
		return nil, fmt.Errorf("invite secret required")
	}
	if cfg.Invite && cfg.Peer.ID == cfg.ID && cfg.Peer.ID != "" {
		return nil, fmt.Errorf("peer id must differ")
	}
	local, prefix, err := parseIPv4(cfg.CIDR)
	if err != nil {
		return nil, err
	}
	peerIP := cfg.Peer.IP.To4()
	if peerIP == nil {
		return nil, fmt.Errorf("peer IPv4 required")
	}
	if !prefix.Contains(peerIP) {
		return nil, fmt.Errorf("peer IP %s is outside %s", peerIP, prefix)
	}
	if peerIP.Equal(local) {
		return nil, fmt.Errorf("peer IP must differ from local IP")
	}
	if cfg.Listen == "" {
		cfg.Listen = "0.0.0.0:0"
	}
	cfg.Peer.IP = peerIP
	return &Node{cfg: cfg, local: local, prefix: prefix}, nil
}

// LocalIP is the overlay address of this node.
func (n *Node) LocalIP() net.IP { return n.local }

// PeerIP is the overlay address of the peer.
func (n *Node) PeerIP() net.IP { return n.cfg.Peer.IP }

// DeviceName is the interface name of the device. It is empty until Start.
func (n *Node) DeviceName() string {
	if n.dev == nil {
		return ""
	}
	return n.dev.Name()
}

// Path reports the transport that carries the peer connection. It stays empty
// until the peer is connected.
func (n *Node) Path() transport.Kind {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.peerKind
}

// ListenAddr is the address the QUIC listener bound. It is empty until Start.
func (n *Node) ListenAddr() string {
	if n.ln == nil {
		return ""
	}
	return n.ln.Addr().String()
}

// Start opens the device and the QUIC listener, offers to the relays, and then
// moves packets between the device and the peer. It needs root or CAP_NET_ADMIN
// unless Config.Device is set, and it returns as soon as the node runs.
func (n *Node) Start(ctx context.Context) error {
	ctx, n.cancel = context.WithCancel(ctx)
	if n.cfg.Device != nil {
		n.dev = n.cfg.Device
	} else {
		dev, err := openTUN("")
		if err != nil {
			return err
		}
		if err := configureTUN(dev.Name(), n.local, n.cfg.Peer.IP, n.prefix.Mask); err != nil {
			_ = dev.Close()
			return err
		}
		n.dev = dev
	}

	ln, _, err := transport.ListenQUIC(n.cfg.Listen, n.cfg.Cert, n.allowFP)
	if err != nil {
		n.cleanupDev()
		return err
	}
	n.ln = ln

	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		n.acceptLoop(ctx)
	}()
	for _, url := range n.cfg.Relays {
		url := url
		if url == "" || url == "off" {
			continue
		}
		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			_ = relay.Offer(ctx, url, n.cfg.ID, func(ticket, _ string) {
				n.wg.Add(1)
				go func() {
					defer n.wg.Done()
					n.acceptRelay(ctx, url, ticket)
				}()
			})
		}()
	}
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		n.dialLoop(ctx)
	}()
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		n.tunLoop(ctx)
	}()
	return nil
}

// Close drops the peer, closes the device, and waits for the background work.
// It also stops a node whose Start returned an error.
func (n *Node) Close() error {
	if n.cancel != nil {
		n.cancel()
	}
	if n.ln != nil {
		_ = n.ln.Close()
	}
	n.dropPeer()
	n.closeDev()
	n.waitGoroutines()
	n.dev = nil
	return nil
}

// shutdownGrace bounds the wait for the node goroutines. tunLoop reads from
// the TUN with a blocking read, because the Go poller does not see packets on
// an Orb virtio TUN, and closing the file descriptor does not wake a read that
// is already blocked on Linux. Without a bound, Close waits for the next packet
// to arrive, so a shutdown never finishes and the interface outlives the
// command that should have removed it.
const shutdownGrace = 2 * time.Second

// waitGoroutines waits for the node goroutines, giving up after
// shutdownGrace. A goroutine still blocked on a TUN read ends when the process
// exits, which releases the interface anyway.
func (n *Node) waitGoroutines() {
	done := make(chan struct{})
	go func() {
		n.wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(shutdownGrace)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func (n *Node) cleanupDev() error {
	if n.dev == nil {
		return nil
	}
	err := n.closeDev()
	n.dev = nil
	return err
}

// closeDev closes the TUN and drops the firewall rules that name it. The
// device name is read before the close, because a closed device no longer
// reports one.
//
// The caller clears n.dev, not this function: tunLoop and the send path read
// that field without a nil check, so it has to stay valid until the callers
// decide the node is finished with it.
func (n *Node) closeDev() error {
	name := n.dev.Name()
	err := n.dev.Close()
	removeLinuxTUNFirewall(name)
	return err
}

func (n *Node) logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "enserie "+n.cfg.ID+": "+format+"\n", args...)
}

func (n *Node) allowFP(fp string) bool {
	if n.cfg.Invite {
		return transport.NormalizeFP(fp) != ""
	}
	return transport.NormalizeFP(fp) == transport.NormalizeFP(n.cfg.Peer.CertFP)
}

func (n *Node) acceptLoop(ctx context.Context) {
	for {
		c, err := n.ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				if ctx.Err() != nil {
					return
				}
				time.Sleep(50 * time.Millisecond)
				continue
			}
		}
		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			n.serveConn(ctx, c, transport.KindQUIC)
		}()
	}
}

func (n *Node) acceptRelay(ctx context.Context, relayURL, ticket string) {
	raw, err := relay.Accept(ctx, relayURL, ticket)
	if err != nil {
		n.logf("relay accept: %v", err)
		return
	}
	secure, err := transport.ServerE2E(raw, transport.E2EServerConfig(n.cfg.Cert, n.allowFP))
	if err != nil {
		n.logf("relay server tls: %v", err)
		_ = raw.Close()
		return
	}
	n.serveConn(ctx, transport.Wrap(secure, transport.Info{Transport: transport.KindRelay, TLS: true}), transport.KindRelay)
}

func (n *Node) dialLoop(ctx context.Context) {
	// Only one side dials. Simultaneous dials through the relay pick
	// opposite splices and then each close the other's live path.
	if n.cfg.Invite {
		<-ctx.Done()
		return
	}
	if !n.cfg.AlwaysDial && n.cfg.ID <= n.cfg.Peer.ID {
		<-ctx.Done()
		return
	}
	backoff := 50 * time.Millisecond
	for {
		if ctx.Err() != nil {
			return
		}
		if n.hasPeer() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
			continue
		}
		conn, kind, err := n.dialPeer(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = 200 * time.Millisecond
		n.serveConn(ctx, conn, kind)
	}
}

func (n *Node) dialPeer(ctx context.Context) (net.Conn, transport.Kind, error) {
	if !n.cfg.Peer.ForceRelay {
		for _, addr := range transport.PreferNonLoopback(n.cfg.Peer.Candidates) {
			c, err := transport.DialQUIC(ctx, addr, n.cfg.Peer.CertFP, n.cfg.Cert)
			if err == nil {
				return c, transport.KindQUIC, nil
			}
		}
	}
	var errs []error
	for _, url := range n.cfg.Relays {
		if url == "" || url == "off" {
			continue
		}
		raw, err := relay.Dial(ctx, url, n.cfg.Peer.ID)
		if err != nil {
			n.logf("relay dial %s: %v", url, err)
			errs = append(errs, err)
			continue
		}
		secure, err := transport.ClientE2E(raw, transport.E2EClientConfig(n.cfg.Cert, n.cfg.Peer.CertFP))
		if err != nil {
			n.logf("relay client tls: %v", err)
			_ = raw.Close()
			errs = append(errs, err)
			continue
		}
		return transport.Wrap(secure, transport.Info{Transport: transport.KindRelay, TLS: true}), transport.KindRelay, nil
	}
	if len(errs) == 0 {
		return nil, "", fmt.Errorf("no QUIC candidates and no relay")
	}
	return nil, "", errors.Join(errs...)
}

func (n *Node) serveConn(ctx context.Context, c net.Conn, kind transport.Kind) {
	defer c.Close()
	// Do not SetDeadline on this conn. websocket.NetConn often keeps the
	// first deadline, which kills the path ~15s after hello.
	errCh := make(chan error, 1)
	go func() { errCh <- n.handshakePeer(c) }()
	timer := time.NewTimer(helloTimeout)
	defer timer.Stop()
	select {
	case err := <-errCh:
		if err != nil {
			n.logf("%v", err)
			return
		}
	case <-timer.C:
		n.logf("hello timeout")
		return
	case <-ctx.Done():
		return
	}
	if !n.installPeer(c, kind) {
		n.logf("peer already connected, drop %s", kind)
		return
	}
	// The invite token has now done its one job. A later holder of the same
	// token must not pair under a different id.
	n.markPaired()
	n.logf("path %s", kind)
	defer n.clearPeer(c)
	for {
		if ctx.Err() != nil {
			return
		}
		typ, payload, err := readFrame(c)
		if err != nil {
			return
		}
		if typ != typePacket {
			continue
		}
		payload = stripTUNPI(payload)
		if _, err := n.dev.WritePacket(payload); err != nil && !errors.Is(err, net.ErrClosed) {
			return
		}
	}
}

const helloTimeout = 15 * time.Second

func (n *Node) handshakePeer(c net.Conn) error {
	if err := writeHello(c, n.cfg.ID, n.cfg.Secret); err != nil {
		return fmt.Errorf("hello write: %w", err)
	}
	id, secret, err := readHello(c)
	if err != nil {
		return fmt.Errorf("hello read: %w", err)
	}
	if n.cfg.Secret != "" && secret != n.cfg.Secret {
		return fmt.Errorf("hello secret mismatch")
	}
	if n.cfg.Invite {
		if !n.bindPeerID(id) {
			return fmt.Errorf("hello id %q rejected", id)
		}
	} else if id != n.cfg.Peer.ID {
		return fmt.Errorf("hello id %q != %q", id, n.cfg.Peer.ID)
	}
	return nil
}

func (n *Node) tunLoop(ctx context.Context) {
	buf := make([]byte, overlayMTU+64)
	for {
		if ctx.Err() != nil {
			return
		}
		nr, err := n.dev.ReadPacket(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				if ctx.Err() != nil {
					return
				}
				time.Sleep(20 * time.Millisecond)
				continue
			}
		}
		pkt := append([]byte(nil), stripTUNPI(buf[:nr])...)
		if _, err := ipv4Dest(pkt); err != nil {
			continue
		}
		n.mu.Lock()
		c := n.peerConn
		n.mu.Unlock()
		if c == nil {
			continue
		}
		if err := writeFrame(c, typePacket, pkt); err != nil {
			n.logf("path write: %v", err)
			n.dropPeer()
		}
	}
}

func (n *Node) hasPeer() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.peerConn != nil
}

func (n *Node) bindPeerID(id string) bool {
	if id == "" || id == n.cfg.ID {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	// An invite token pairs one node and no other. Without this a second
	// holder of the same token could pair under a different id, because the
	// bound id is the only thing that tells two peers apart.
	if n.paired {
		return n.cfg.Peer.ID == id
	}
	if n.cfg.Peer.ID == "" {
		n.cfg.Peer.ID = id
		return true
	}
	return n.cfg.Peer.ID == id
}

// markPaired records that the invite token has paired this node, so a later
// holder of the same token cannot pair under a different id.
func (n *Node) markPaired() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.paired = true
}

func (n *Node) installPeer(c net.Conn, kind transport.Kind) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.peerConn != nil {
		return false
	}
	n.peerConn = c
	n.peerKind = kind
	return true
}

func (n *Node) clearPeer(c net.Conn) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.peerConn == c {
		n.peerConn = nil
		n.peerKind = ""
	}
}

func (n *Node) dropPeer() {
	n.mu.Lock()
	c := n.peerConn
	n.peerConn = nil
	n.peerKind = ""
	n.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// Inject writes a packet as if the local stack had written it to the device.
func (n *Node) Inject(pkt []byte) error {
	if n.dev == nil {
		return io.ErrClosedPipe
	}
	// Tests push a packet as if the local kernel wrote it to TUN.
	if inj, ok := n.dev.(*memDevice); ok {
		return inj.injectLocal(pkt)
	}
	_, err := n.dev.WritePacket(pkt)
	return err
}

// WaitPacket returns the next packet that the peer sent. It only reads the
// memory device of this package, so it stays inside its tests.
func (n *Node) WaitPacket(timeout time.Duration) ([]byte, error) {
	if inj, ok := n.dev.(*memDevice); ok {
		return inj.waitRemote(timeout)
	}
	return nil, fmt.Errorf("WaitPacket requires a memory device")
}

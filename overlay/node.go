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
	ID         string
	IP         net.IP
	CertFP     string
	Candidates []string
	ForceRelay bool
}

type Config struct {
	ID     string
	CIDR   string // local overlay address, e.g. 10.7.0.1/30
	Cert   tls.Certificate
	Listen string // QUIC listen, default 0.0.0.0:0
	Relays []string
	Peer   Peer
	Device Device // nil opens a real TUN
	Secret string // shared invite secret; empty skips the check
	// Invite listens and offers. It does not dial. The first hello with
	// a matching secret becomes the peer.
	Invite bool
	// AlwaysDial makes this node the dialer even when its id is smaller.
	AlwaysDial bool
}

type Node struct {
	cfg      Config
	local    net.IP
	prefix   *net.IPNet
	dev      Device
	mu       sync.Mutex
	peerConn net.Conn
	peerKind transport.Kind
	ln       net.Listener
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

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

func (n *Node) LocalIP() net.IP { return n.local }
func (n *Node) PeerIP() net.IP  { return n.cfg.Peer.IP }

func (n *Node) Path() transport.Kind {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.peerKind
}

func (n *Node) ListenAddr() string {
	if n.ln == nil {
		return ""
	}
	return n.ln.Addr().String()
}

func (n *Node) Start(ctx context.Context) error {
	ctx, n.cancel = context.WithCancel(ctx)
	if n.cfg.Device != nil {
		n.dev = n.cfg.Device
	} else {
		dev, err := openTUN("enserie0")
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

func (n *Node) Close() error {
	if n.cancel != nil {
		n.cancel()
	}
	if n.ln != nil {
		_ = n.ln.Close()
	}
	n.dropPeer()
	if n.dev != nil {
		_ = n.dev.Close()
	}
	n.wg.Wait()
	n.dev = nil
	return nil
}

func (n *Node) cleanupDev() error {
	if n.dev == nil {
		return nil
	}
	err := n.dev.Close()
	n.dev = nil
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
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	if err := writeHello(c, n.cfg.ID, n.cfg.Secret); err != nil {
		n.logf("hello write: %v", err)
		return
	}
	id, secret, err := readHello(c)
	_ = c.SetDeadline(time.Time{})
	if err != nil {
		n.logf("hello read: %v", err)
		return
	}
	if n.cfg.Secret != "" && secret != n.cfg.Secret {
		n.logf("hello secret mismatch")
		return
	}
	if n.cfg.Invite {
		if !n.bindPeerID(id) {
			n.logf("hello id %q rejected", id)
			return
		}
	} else if id != n.cfg.Peer.ID {
		n.logf("hello id %q != %q", id, n.cfg.Peer.ID)
		return
	}
	if !n.installPeer(c, kind) {
		n.logf("peer already connected, drop %s", kind)
		return
	}
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
		if _, err := n.dev.WritePacket(payload); err != nil && !errors.Is(err, net.ErrClosed) {
			return
		}
	}
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
		pkt := buf[:nr]
		dst, err := ipv4Dest(pkt)
		if err != nil {
			continue
		}
		if !dst.Equal(n.cfg.Peer.IP) {
			continue
		}
		n.mu.Lock()
		c := n.peerConn
		n.mu.Unlock()
		if c == nil {
			continue
		}
		_ = writeFrame(c, typePacket, pkt)
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
	if n.cfg.Peer.ID == "" {
		n.cfg.Peer.ID = id
		return true
	}
	return n.cfg.Peer.ID == id
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

func (n *Node) WaitPacket(timeout time.Duration) ([]byte, error) {
	if inj, ok := n.dev.(*memDevice); ok {
		return inj.waitRemote(timeout)
	}
	return nil, fmt.Errorf("WaitPacket requires a memory device")
}

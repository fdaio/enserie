package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fdaio/enserie/overlay"
	"github.com/fdaio/enserie/proxy"
	"github.com/fdaio/enserie/transport"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "--version", "-version":
		fmt.Println(version)
	case "invite":
		opts, err := parseInviteFlags(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := maybeSupervise(func() error { return runInvite(opts) }); err != nil {
			reportFailure(err)
			os.Exit(1)
		}
	case "accept":
		opts, token, err := parseAcceptFlags(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := maybeSupervise(func() error { return runAccept(token, opts) }); err != nil {
			reportFailure(err)
			os.Exit(1)
		}
	case "down":
		if err := runDown(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "status":
		// A script asks status to find out whether the overlay is up, so an
		// exit code answers that without parsing the message.
		if err := runStatus(os.Stdout); err != nil {
			if errors.Is(err, errNotRunning) {
				fmt.Fprintln(os.Stderr, "ens: not running")
				os.Exit(1)
			}
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ens invite [--tun] [--subnet CIDR] [--advertise HOST] [--socks-listen ADDR]")
	fmt.Fprintln(os.Stderr, "       ens accept [--socks-listen ADDR] TOKEN")
	fmt.Fprintln(os.Stderr, "       ens down")
	fmt.Fprintln(os.Stderr, "       ens status")
	fmt.Fprintln(os.Stderr, "       ens version")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "The default mode is a local SOCKS5 proxy and needs no root.")
	fmt.Fprintln(os.Stderr, "--tun opens a transparent TUN interface and needs root.")
}

// defaultSocksListen is the loopback address of the proxy mode listener.
const defaultSocksListen = proxy.DefaultListen

type inviteOptions struct {
	subnet      string
	advertise   string
	mode        string
	socksListen string
}

// parseInviteFlags reads the options for ens invite. The overlay range is
// fixed by default, so a caller passes --subnet only when a VPN or another
// network already holds the default range.
func parseInviteFlags(args []string) (inviteOptions, error) {
	fs := flag.NewFlagSet("invite", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	subnet := fs.String("subnet", "", "IPv4 network to take the overlay link from, e.g. 10.99.0.0/24")
	advertise := fs.String("advertise", "", "address the peer should try first, e.g. 203.0.113.7 or home.example.com")
	tun := fs.Bool("tun", false, "open a TUN interface (needs root) instead of the SOCKS5 proxy")
	useProxy := fs.Bool("proxy", false, "run the zero-privilege SOCKS5 proxy (default)")
	socksListen := fs.String("socks-listen", defaultSocksListen, "SOCKS5 listen address in proxy mode")
	if err := fs.Parse(args); err != nil {
		return inviteOptions{}, fmt.Errorf("%w\n\n%s", err, inviteFlagHelp())
	}
	if *tun && *useProxy {
		return inviteOptions{}, fmt.Errorf("--tun and --proxy are mutually exclusive\n\n%s", inviteFlagHelp())
	}
	opts := inviteOptions{
		subnet:      *subnet,
		advertise:   *advertise,
		mode:        overlay.ModeProxy,
		socksListen: *socksListen,
	}
	if *tun {
		opts.mode = overlay.ModeTun
	}
	// A flag package reads "--subnet --relay" as the subnet "--relay" and
	// leaves the rest as positional arguments, so check the value first and
	// name the option rather than reporting a broken CIDR.
	if opts.subnet != "" && strings.HasPrefix(opts.subnet, "-") {
		return inviteOptions{}, fmt.Errorf("--subnet needs a network, got the option %q\n\n%s", opts.subnet, inviteFlagHelp())
	}
	if opts.advertise != "" && strings.HasPrefix(opts.advertise, "-") {
		return inviteOptions{}, fmt.Errorf("--advertise needs an address, got the option %q\n\n%s", opts.advertise, inviteFlagHelp())
	}
	if opts.advertise != "" {
		host, _, err := net.SplitHostPort(opts.advertise)
		if err == nil {
			// A port was given but the listener picks its own, so keeping it
			// would advertise a port nothing is listening on.
			opts.advertise = host
		}
		if strings.TrimSpace(opts.advertise) == "" {
			return inviteOptions{}, fmt.Errorf("--advertise needs an address\n\n%s", inviteFlagHelp())
		}
	}
	if opts.mode == overlay.ModeProxy {
		if _, _, err := net.SplitHostPort(opts.socksListen); err != nil {
			return inviteOptions{}, fmt.Errorf("--socks-listen needs HOST:PORT, got %q\n\n%s", opts.socksListen, inviteFlagHelp())
		}
	} else {
		explicit := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "socks-listen" {
				explicit = true
			}
		})
		if explicit {
			return inviteOptions{}, fmt.Errorf("--socks-listen is for proxy mode; drop --tun\n\n%s", inviteFlagHelp())
		}
	}
	if fs.NArg() > 0 {
		return inviteOptions{}, fmt.Errorf("ens invite takes no positional argument\n\n%s", inviteFlagHelp())
	}
	return opts, nil
}

func inviteFlagHelp() string {
	var b strings.Builder
	fs := flag.NewFlagSet("invite", flag.ContinueOnError)
	fs.String("subnet", "", "IPv4 network to take the overlay link from, e.g. 10.99.0.0/24")
	fs.String("advertise", "", "address the peer should try first, e.g. 203.0.113.7 or home.example.com")
	fs.Bool("tun", false, "open a TUN interface (needs root) instead of the SOCKS5 proxy")
	fs.Bool("proxy", false, "run the zero-privilege SOCKS5 proxy (default)")
	fs.String("socks-listen", defaultSocksListen, "SOCKS5 listen address in proxy mode")
	fmt.Fprintf(&b, "usage: ens invite [--tun] [--subnet CIDR] [--advertise HOST] [--socks-listen ADDR]\n")
	old := fs.Output()
	fs.SetOutput(&b)
	fs.PrintDefaults()
	fs.SetOutput(old)
	return b.String()
}

type acceptOptions struct {
	socksListen string
}

func parseAcceptFlags(args []string) (acceptOptions, string, error) {
	fs := flag.NewFlagSet("accept", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	socksListen := fs.String("socks-listen", defaultSocksListen, "SOCKS5 listen address in proxy mode")
	if err := fs.Parse(args); err != nil {
		return acceptOptions{}, "", fmt.Errorf("%w\n\n%s", err, acceptFlagHelp())
	}
	if fs.NArg() != 1 {
		return acceptOptions{}, "", fmt.Errorf("ens accept needs exactly one token\n\n%s", acceptFlagHelp())
	}
	if _, _, err := net.SplitHostPort(*socksListen); err != nil {
		return acceptOptions{}, "", fmt.Errorf("--socks-listen needs HOST:PORT, got %q\n\n%s", *socksListen, acceptFlagHelp())
	}
	return acceptOptions{socksListen: *socksListen}, fs.Arg(0), nil
}

func acceptFlagHelp() string {
	var b strings.Builder
	fs := flag.NewFlagSet("accept", flag.ContinueOnError)
	fs.String("socks-listen", defaultSocksListen, "SOCKS5 listen address in proxy mode")
	fmt.Fprintf(&b, "usage: ens accept [--socks-listen ADDR] TOKEN\n")
	old := fs.Output()
	fs.SetOutput(&b)
	fs.PrintDefaults()
	fs.SetOutput(old)
	return b.String()
}

// reportFailure prints err once. A worker that fails before the path is up
// already sent the error over the status pipe, and the supervisor prints that
// copy, so printing it in the worker as well showed the same failure twice.
func reportFailure(err error) {
	if isWorker() {
		return
	}
	fmt.Fprintln(os.Stderr, err)
}

func runInvite(opts inviteOptions) error {
	lock, err := acquireInstanceLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	id, err := overlay.RandomID()
	if err != nil {
		return err
	}
	secret, err := overlay.RandomSecret()
	if err != nil {
		return err
	}
	inviteCIDR, err := overlay.RandomLink()
	if opts.subnet != "" {
		inviteCIDR, err = overlay.RandomLinkIn(opts.subnet)
	}
	if err != nil {
		return err
	}
	peerCIDR, err := overlay.OtherCIDR(inviteCIDR)
	if err != nil {
		return err
	}
	peerIP, _, err := parseCIDR(peerCIDR)
	if err != nil {
		return err
	}
	localIP, _, err := parseCIDR(inviteCIDR)
	if err != nil {
		return err
	}
	cert, err := ephemeralCert()
	if err != nil {
		return err
	}
	fp, err := transport.CertFingerprint(cert)
	if err != nil {
		return err
	}
	cfg := overlay.Config{
		ID:     id,
		CIDR:   inviteCIDR,
		Cert:   cert,
		Listen: "0.0.0.0:0",
		Relays: overlay.DefaultRelayURLs(),
		Peer:   overlay.Peer{IP: peerIP},
		Secret: secret,
		Invite: true,
	}
	var ps *proxy.Server
	if opts.mode == overlay.ModeProxy {
		ps, err = newProxy(opts.socksListen, localIP)
		if err != nil {
			return err
		}
		defer ps.Close()
		cfg.OnPeer = func(ctx context.Context, c net.Conn, kind transport.Kind) error {
			return ps.HandlePeer(ctx, c, true)
		}
	}
	n, err := overlay.New(cfg)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := n.Start(ctx); err != nil {
		return wrapStart(err, opts.mode)
	}
	defer n.Close()

	_, prefix, err := parseCIDR(inviteCIDR)
	if err != nil {
		return err
	}
	inv := overlay.Invite{
		V:      1,
		Mode:   opts.mode,
		ID:     id,
		FP:     fp,
		CIDR:   inviteCIDR,
		Addrs:  overlay.FilterOverlayAddrs(transport.ExpandCandidates(n.ListenAddr(), opts.advertise), prefix),
		Secret: secret,
	}
	token, err := inv.Encode()
	if err != nil {
		return err
	}
	if opts.mode == overlay.ModeProxy {
		fmt.Fprintf(os.Stderr, "ens: proxy %s overlay %s  peer will be %s\n", ps.Addr(), n.LocalIP(), n.PeerIP())
		fmt.Fprintf(os.Stderr, "ens: through the proxy, %s:PORT reaches the peer's loopback\n", n.PeerIP())
	} else {
		fmt.Fprintf(os.Stderr, "ens: tun %s overlay %s  peer will be %s\n", n.DeviceName(), n.LocalIP(), n.PeerIP())
	}
	fmt.Fprintf(os.Stderr, "ens: on the other machine run:\n\n")
	fmt.Printf("%s\n\n", acceptCommand(token, opts.mode))
	go reportPath(ctx, n)
	<-ctx.Done()
	return nil
}

func runAccept(token string, opts acceptOptions) error {
	lock, err := acquireInstanceLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	inv, err := overlay.ParseInvite(token)
	if err != nil {
		return err
	}
	cidr, err := overlay.OtherCIDR(inv.CIDR)
	if err != nil {
		return err
	}
	localIP, _, err := parseCIDR(cidr)
	if err != nil {
		return err
	}
	id, err := overlay.RandomID()
	if err != nil {
		return err
	}
	cert, err := ephemeralCert()
	if err != nil {
		return err
	}
	peerIP, _, err := parseCIDR(inv.CIDR)
	if err != nil {
		return err
	}
	cfg := overlay.Config{
		ID:     id,
		CIDR:   cidr,
		Cert:   cert,
		Listen: "0.0.0.0:0",
		Relays: inv.Relays,
		Peer: overlay.Peer{
			ID:         inv.ID,
			IP:         peerIP,
			CertFP:     inv.FP,
			Candidates: inv.Addrs,
		},
		Secret:     inv.Secret,
		AlwaysDial: true,
	}
	var ps *proxy.Server
	if inv.Mode == overlay.ModeProxy {
		ps, err = newProxy(opts.socksListen, localIP)
		if err != nil {
			return err
		}
		defer ps.Close()
		cfg.OnPeer = func(ctx context.Context, c net.Conn, kind transport.Kind) error {
			return ps.HandlePeer(ctx, c, false)
		}
	}
	n, err := overlay.New(cfg)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := n.Start(ctx); err != nil {
		return wrapStart(err, inv.Mode)
	}
	defer n.Close()
	if inv.Mode == overlay.ModeProxy {
		fmt.Fprintf(os.Stderr, "ens: proxy %s overlay %s  peer %s\n", ps.Addr(), n.LocalIP(), n.PeerIP())
		fmt.Fprintf(os.Stderr, "ens: through the proxy, %s:PORT reaches the peer's loopback\n", n.PeerIP())
	} else {
		fmt.Fprintf(os.Stderr, "ens: tun %s overlay %s  peer %s\n", n.DeviceName(), n.LocalIP(), n.PeerIP())
	}
	go reportPath(ctx, n)
	<-ctx.Done()
	return nil
}

// newProxy starts the local SOCKS5 listener. It runs before the peer path is
// up, so a request that arrives too early gets "network unreachable" instead
// of a refused connection.
func newProxy(addr string, localIP net.IP) (*proxy.Server, error) {
	ps := proxy.New(addr, func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "ens: "+format+"\n", args...)
	})
	ps.LocalIP = localIP
	if err := ps.Start(); err != nil {
		return nil, err
	}
	return ps, nil
}

func reportPath(ctx context.Context, n *overlay.Node) {
	for {
		if k := n.Path(); k != "" {
			fmt.Fprintf(os.Stderr, "ens: connected via %s\n", k)
			if isWorker() {
				redirectLogs()
				notifyConnected()
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func ephemeralCert() (tls.Certificate, error) {
	dir, err := os.MkdirTemp("", "ens-")
	if err != nil {
		return tls.Certificate{}, err
	}
	// EnsureServerCert returns a certificate that is already in memory, so the
	// files it wrote are dead weight. Keeping them would leave one directory
	// holding a private key behind on every run.
	defer func() { _ = os.RemoveAll(dir) }()
	return transport.EnsureServerCert(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"))
}

func parseCIDR(cidr string) (net.IP, *net.IPNet, error) {
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, nil, err
	}
	v4 := ip.To4()
	if v4 == nil {
		return nil, nil, fmt.Errorf("overlay requires IPv4, got %s", cidr)
	}
	return v4, n, nil
}

// acceptCommand renders the command the peer runs. It names no path, because
// the peer may have installed somewhere else or not at all. Proxy mode needs no
// privilege. TUN mode prefixes sudo; the peer's shell resolves the bare name
// before sudo drops PATH.
func acceptCommand(token, mode string) string {
	if mode == overlay.ModeProxy {
		return "ens accept " + token
	}
	return "sudo ens accept " + token
}

func wrapStart(err error, mode string) error {
	if mode == overlay.ModeProxy {
		return fmt.Errorf("start: %w", err)
	}
	if os.Geteuid() != 0 {
		// Naming the command matters more than saying "need root": a reader
		// who is told only that retries as root still types "sudo ens", which
		// a Homebrew install cannot resolve.
		return fmt.Errorf("tun/listen: %w (need root); run it as root: %s", err, sudoCommand(os.Args[1:]...))
	}
	return fmt.Errorf("tun/listen: %w", err)
}

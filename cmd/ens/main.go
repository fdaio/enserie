package main

import (
	"context"
	"crypto/tls"
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
		subnet, err := parseInviteFlags(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := maybeSupervise(func() error { return runInvite(subnet) }); err != nil {
			reportFailure(err)
			os.Exit(1)
		}
	case "accept":
		if len(os.Args) < 3 {
			usage()
			os.Exit(2)
		}
		if err := maybeSupervise(func() error { return runAccept(os.Args[2]) }); err != nil {
			reportFailure(err)
			os.Exit(1)
		}
	case "down":
		if err := runDown(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ens invite [--subnet CIDR]")
	fmt.Fprintln(os.Stderr, "       ens accept TOKEN")
	fmt.Fprintln(os.Stderr, "       ens down")
	fmt.Fprintln(os.Stderr, "       ens version")
}

// parseInviteFlags reads the options for ens invite. The overlay range is
// fixed by default, so a caller passes --subnet only when a VPN or another
// network already holds the default range.
func parseInviteFlags(args []string) (string, error) {
	fs := flag.NewFlagSet("invite", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	subnet := fs.String("subnet", "", "IPv4 network to take the overlay link from, e.g. 10.99.0.0/24")
	if err := fs.Parse(args); err != nil {
		return "", fmt.Errorf("%w\n\n%s", err, inviteFlagHelp())
	}
	// A flag package reads "--subnet --relay" as the subnet "--relay" and
	// leaves the rest as positional arguments, so check the value first and
	// name the option rather than reporting a broken CIDR.
	if *subnet != "" && strings.HasPrefix(*subnet, "-") {
		return "", fmt.Errorf("--subnet needs a network, got the option %q\n\n%s", *subnet, inviteFlagHelp())
	}
	if fs.NArg() > 0 {
		return "", fmt.Errorf("ens invite takes no positional argument\n\n%s", inviteFlagHelp())
	}
	return *subnet, nil
}

func inviteFlagHelp() string {
	var b strings.Builder
	fs := flag.NewFlagSet("invite", flag.ContinueOnError)
	fs.String("subnet", "", "IPv4 network to take the overlay link from, e.g. 10.99.0.0/24")
	fmt.Fprintf(&b, "usage: ens invite [--subnet CIDR]\n")
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

func runInvite(subnet string) error {
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
	if subnet != "" {
		inviteCIDR, err = overlay.RandomLinkIn(subnet)
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
	cert, err := ephemeralCert()
	if err != nil {
		return err
	}
	fp, err := transport.CertFingerprint(cert)
	if err != nil {
		return err
	}
	n, err := overlay.New(overlay.Config{
		ID:     id,
		CIDR:   inviteCIDR,
		Cert:   cert,
		Listen: "0.0.0.0:0",
		Relays: overlay.DefaultRelayURLs(),
		Peer:   overlay.Peer{IP: peerIP},
		Secret: secret,
		Invite: true,
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := n.Start(ctx); err != nil {
		return wrapStart(err)
	}
	defer n.Close()

	_, prefix, err := parseCIDR(inviteCIDR)
	if err != nil {
		return err
	}
	inv := overlay.Invite{
		V:      1,
		ID:     id,
		FP:     fp,
		CIDR:   inviteCIDR,
		Addrs:  overlay.FilterOverlayAddrs(transport.ExpandCandidates(n.ListenAddr(), ""), prefix),
		Secret: secret,
	}
	token, err := inv.Encode()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "ens: tun %s overlay %s  peer will be %s\n", n.DeviceName(), n.LocalIP(), n.PeerIP())
	fmt.Fprintf(os.Stderr, "ens: on the other machine run:\n\n")
	fmt.Printf("sudo ens accept %s\n\n", token)
	go reportPath(ctx, n)
	<-ctx.Done()
	return nil
}

func runAccept(token string) error {
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
	n, err := overlay.New(overlay.Config{
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
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := n.Start(ctx); err != nil {
		return wrapStart(err)
	}
	defer n.Close()
	fmt.Fprintf(os.Stderr, "ens: tun %s overlay %s  peer %s\n", n.DeviceName(), n.LocalIP(), n.PeerIP())
	go reportPath(ctx, n)
	<-ctx.Done()
	return nil
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

func wrapStart(err error) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("tun/listen: %w (need root)", err)
	}
	return fmt.Errorf("tun/listen: %w", err)
}

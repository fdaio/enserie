package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
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
		if err := runInvite(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "accept":
		if len(os.Args) < 3 {
			usage()
			os.Exit(2)
		}
		if err := runAccept(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ens invite")
	fmt.Fprintln(os.Stderr, "       ens accept TOKEN")
	fmt.Fprintln(os.Stderr, "       ens version")
}

func runInvite() error {
	id, err := overlay.RandomID()
	if err != nil {
		return err
	}
	secret, err := overlay.RandomSecret()
	if err != nil {
		return err
	}
	inviteCIDR, err := overlay.RandomLink()
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
		return fmt.Errorf("tun/listen: %w (need root)", err)
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
	fmt.Fprintf(os.Stderr, "ens: overlay %s  peer will be %s\n", n.LocalIP(), n.PeerIP())
	fmt.Fprintf(os.Stderr, "ens: on the other machine run:\n\n")
	fmt.Printf("sudo ens accept %s\n\n", token)
	go reportPath(ctx, n)
	<-ctx.Done()
	return nil
}

func runAccept(token string) error {
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
		return fmt.Errorf("tun/listen: %w (need root)", err)
	}
	defer n.Close()
	fmt.Fprintf(os.Stderr, "ens: overlay %s  peer %s\n", n.LocalIP(), n.PeerIP())
	go reportPath(ctx, n)
	<-ctx.Done()
	return nil
}

func reportPath(ctx context.Context, n *overlay.Node) {
	for {
		if k := n.Path(); k != "" {
			fmt.Fprintf(os.Stderr, "ens: connected via %s\n", k)
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

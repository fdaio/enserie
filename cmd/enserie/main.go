package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fdaio/enserie/overlay"
	"github.com/fdaio/enserie/transport"
)

// version is set at link time from the Git tag (v0.1.0 -> 0.1.0).
var version = "dev"

// tyd's hosted relays speak the same splice protocol. --relay off disables them.
const defaultRelays = "https://relay-1.getfda.dev,https://relay-2.getfda.dev"

func main() {
	id := flag.String("id", "", "this node id")
	cidr := flag.String("ip", "", "overlay CIDR for this node, e.g. 10.7.0.1/30")
	peer := flag.String("peer", "", "peer as id=ip, e.g. z=10.7.0.2")
	peerFP := flag.String("peer-fp", "", "peer certificate SHA-256 fingerprint (hex)")
	peerAddr := flag.String("peer-addr", "", "QUIC candidate host:port (comma-separated)")
	relay := flag.String("relay", defaultRelays, "relay URL list, comma-separated; off disables fallback")
	dir := flag.String("dir", "", "state directory for the TLS certificate")
	listen := flag.String("listen", "0.0.0.0:0", "QUIC listen address")
	advertise := flag.String("advertise", "", "extra host to print as a QUIC candidate")
	forceRelay := flag.Bool("force-relay", false, "skip QUIC and use relay only")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	if *id == "" || *cidr == "" || *peer == "" {
		fmt.Fprintln(os.Stderr, "usage: enserie --id a --ip 10.7.0.1/30 --peer z=10.7.0.2 --peer-fp HEX [--relay URL]")
		os.Exit(2)
	}
	peerID, peerIP, err := parsePeer(*peer)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	stateDir := *dir
	if stateDir == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			home = os.TempDir()
		}
		stateDir = filepath.Join(home, ".enserie", *id)
	}
	cert, err := transport.EnsureServerCert(filepath.Join(stateDir, "tls.crt"), filepath.Join(stateDir, "tls.key"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fp, err := transport.CertFingerprint(cert)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if strings.TrimSpace(*peerFP) == "" {
		fmt.Fprintf(os.Stderr, "enserie: --peer-fp is required (this node fp %s)\n", fp)
		os.Exit(2)
	}

	n, err := overlay.New(overlay.Config{
		ID:     *id,
		CIDR:   *cidr,
		Cert:   cert,
		Listen: *listen,
		Relays: splitCSV(*relay),
		Peer: overlay.Peer{
			ID:         peerID,
			IP:         peerIP,
			CertFP:     *peerFP,
			Candidates: splitCSV(*peerAddr),
			ForceRelay: *forceRelay,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := n.Start(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "enserie node %s overlay %s peer %s (%s)\n", *id, *cidr, peerID, peerIP)
	fmt.Fprintf(os.Stderr, "enserie cert fp %s\n", fp)
	fmt.Fprintf(os.Stderr, "enserie quic %s\n", n.ListenAddr())
	for _, c := range transport.ExpandCandidates(n.ListenAddr(), *advertise) {
		fmt.Fprintf(os.Stderr, "enserie candidate %s\n", c)
	}
	<-ctx.Done()
	_ = n.Close()
}

func parsePeer(s string) (string, net.IP, error) {
	id, ip, ok := strings.Cut(strings.TrimSpace(s), "=")
	if !ok || id == "" || ip == "" {
		return "", nil, fmt.Errorf("--peer must be id=ip")
	}
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil || parsed.To4() == nil {
		return "", nil, fmt.Errorf("invalid peer IPv4 %q", ip)
	}
	return id, parsed.To4(), nil
}

func splitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

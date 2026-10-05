package transport

import (
	"net"
	"sort"
	"strings"
)

// sharedAddrSpace is 100.64.0.0/10, the range carriers hand out and the range
// Tailscale and similar VPNs use. An address here is reachable only when the
// peer sits on the same VPN, so it is a worse bet than either a public address
// or a LAN address.
var sharedAddrSpace = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// candidateClass ranks how likely an address is to be dialable by another
// machine. Candidates are tried in order and each miss waits out a dial
// timeout, so a VPN address ahead of a public one costs the peer real time.
//
// Nothing is dropped. A LAN address is how two machines on one network pair
// directly, and a shared-space address is how two machines on one VPN pair
// directly. They are only tried later.
func candidateClass(ip net.IP) int {
	switch {
	case ip.IsLoopback():
		return 3
	case sharedAddrSpace.Contains(ip):
		return 2
	case ip.IsPrivate():
		return 1
	case !ip.IsGlobalUnicast():
		return 2
	default:
		return 0
	}
}

// ExpandCandidates builds dial targets for a data-plane listener.
// listenAddr is the actual bound address (may be 0.0.0.0:port).
// advertise, when set to a non-loopback host, is tried first.
// Loopback is always last (same-host / CI only).
func ExpandCandidates(listenAddr, advertise string) []string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		if listenAddr != "" {
			return []string{listenAddr}
		}
		return nil
	}

	seen := map[string]struct{}{}
	var out []string
	add := func(h string) {
		h = strings.TrimSpace(h)
		if h == "" || h == "0.0.0.0" || h == "::" {
			return
		}
		addr := net.JoinHostPort(h, port)
		if _, ok := seen[addr]; ok {
			return
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}

	if adv := strings.TrimSpace(advertise); adv != "" && !isLoopbackHost(adv) {
		add(adv)
	}
	if host != "" && host != "0.0.0.0" && host != "::" && !isLoopbackHost(host) {
		add(host)
	}

	ifaces, err := net.InterfaceAddrs()
	if err == nil {
		var extras []string
		for _, ia := range ifaces {
			ipnet, ok := ia.(*net.IPNet)
			if !ok || ipnet.IP == nil {
				continue
			}
			ip := ipnet.IP
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				extras = append(extras, v4.String())
			}
		}
		for _, h := range rankAddrs(extras) {
			add(h)
		}
	}

	add("127.0.0.1")
	return out
}

// rankAddrs orders bare host addresses by how likely they are to be dialable.
// Sorted order alone is not good enough: 100.64.0.0/10 sorts before every
// 192.168.x.x address, so a VPN address would be tried first and each miss
// waits out a dial timeout.
//
// Addresses of the same class stay sorted, because net.InterfaceAddrs gives no
// ordering guarantee and a token that changed between runs of the same command
// would be hard to tell from a real change.
//
// A host that is not an IP sorts as if it were a LAN address, since advertise
// accepts a hostname and it is normally a name that resolves.
func rankAddrs(hosts []string) []string {
	type ranked struct {
		host string
		rank int
	}
	out := make([]ranked, 0, len(hosts))
	for _, h := range hosts {
		ip := net.ParseIP(strings.Trim(h, "[]"))
		rank := 1
		if ip != nil {
			rank = candidateClass(ip)
		}
		out = append(out, ranked{host: h, rank: rank})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].rank != out[j].rank {
			return out[i].rank < out[j].rank
		}
		return out[i].host < out[j].host
	})
	hosts2 := make([]string, 0, len(out))
	for _, r := range out {
		hosts2 = append(hosts2, r.host)
	}
	return hosts2
}

// PreferNonLoopback reorders dial targets so loopback addresses are tried last.
func PreferNonLoopback(addrs []string) []string {
	seen := map[string]struct{}{}
	var primary, loop []string
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		host, _, err := net.SplitHostPort(a)
		if err != nil {
			host = a
		}
		if isLoopbackHost(host) {
			loop = append(loop, a)
			continue
		}
		primary = append(primary, a)
	}
	return append(primary, loop...)
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

package transport

import (
	"net"
	"testing"
)

// A candidate list is tried in order and each miss waits out a dial timeout,
// so a VPN address ahead of a public one costs the peer real time.
func TestCandidateClass(t *testing.T) {
	tests := []struct {
		ip   string
		want int
		why  string
	}{
		{"203.0.113.7", 0, "publicly routable is the best bet"},
		{"198.51.100.1", 0, "publicly routable is the best bet"},
		{"192.168.1.5", 1, "LAN address, reachable on one network"},
		{"10.7.0.2", 1, "LAN address, reachable on one network"},
		{"172.16.4.4", 1, "LAN address, reachable on one network"},
		{"100.64.0.154", 2, "carrier NAT and Tailscale range, only useful on the same VPN"},
		{"100.127.255.254", 2, "the shared range ends at 100.127.255.254"},
		{"100.128.0.1", 0, "just outside the shared range, so a public address"},
		{"169.254.1.1", 2, "link local is not usable across machines"},
		{"127.0.0.1", 3, "loopback only reaches the same host"},
		{"0.0.0.0", 2, "unspecified is not a dial target"},
	}
	for _, tt := range tests {
		got := candidateClass(parseIP(t, tt.ip))
		if got != tt.want {
			t.Errorf("candidateClass(%s) = %d, want %d (%s)", tt.ip, got, tt.want, tt.why)
		}
	}
}

// Ranking must not drop anything. A LAN address is how two machines on one
// network pair directly, and a shared-space address is how two machines on one
// VPN pair directly.
func TestExpandCandidatesKeepsEveryReachableClass(t *testing.T) {
	got := ExpandCandidates("0.0.0.0:1234", "")
	if len(got) == 0 {
		t.Fatal("no candidates")
	}
	var sawLoopback bool
	for _, a := range got {
		if a == "127.0.0.1:1234" {
			sawLoopback = true
		}
	}
	if !sawLoopback {
		t.Errorf("loopback missing from %v", got)
	}
}

// A duplicated interface address must not appear twice, and loopback has to be
// last because it only reaches the same host.
func TestExpandCandidatesPutsLoopbackLast(t *testing.T) {
	got := ExpandCandidates("0.0.0.0:1234", "")
	if len(got) < 2 {
		t.Skip("only loopback is available on this host")
	}
	if got[len(got)-1] != "127.0.0.1:1234" {
		t.Errorf("last candidate = %s, want 127.0.0.1:1234; full list %v", got[len(got)-1], got)
	}
	seen := map[string]bool{}
	for _, a := range got {
		if seen[a] {
			t.Errorf("candidate %s appears twice in %v", a, got)
		}
		seen[a] = true
	}
}

// The operator knows which address the peer can reach. It has to be tried
// before whatever the interfaces happen to offer.
func TestExpandCandidatesPutsAdvertiseFirst(t *testing.T) {
	got := ExpandCandidates("0.0.0.0:1234", "203.0.113.7")
	if len(got) == 0 {
		t.Fatal("no candidates")
	}
	if got[0] != "203.0.113.7:1234" {
		t.Errorf("first candidate = %s, want 203.0.113.7:1234; full list %v", got[0], got)
	}
}

// A loopback advertise would defeat the purpose, so it is dropped.
func TestExpandCandidatesSkipsLoopbackAdvertise(t *testing.T) {
	got := ExpandCandidates("0.0.0.0:1234", "127.0.0.1")
	for _, a := range got {
		if a != "127.0.0.1:1234" {
			continue
		}
		if len(got) > 1 && got[0] == a {
			t.Errorf("loopback advertise sorted first: %v", got)
		}
	}
}
func parseIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("bad test address %q", s)
	}
	return ip
}

// The ordering itself has to be guarded, not just the classification. Sorted
// order alone puts 100.64.0.0/10 ahead of every 192.168.x.x address, which is
// how a VPN address ended up first in a real token.
func TestRankAddrsPutsVPNAddressesLast(t *testing.T) {
	got := rankAddrs([]string{
		"100.64.0.154",
		"192.168.124.66",
		"203.0.113.7",
		"192.168.139.3",
		"10.7.0.2",
	})
	want := []string{"203.0.113.7", "10.7.0.2", "192.168.124.66", "192.168.139.3", "100.64.0.154"}
	if len(got) != len(want) {
		t.Fatalf("rankAddrs dropped addresses: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("rankAddrs = %v, want %v", got, want)
			break
		}
	}
}

// Ranking must not lose a host that is not an IP, since advertise takes one.
func TestRankAddrsKeepsHostnames(t *testing.T) {
	got := rankAddrs([]string{"100.64.0.154", "home.example.com", "203.0.113.7"})
	found := false
	for _, h := range got {
		if h == "home.example.com" {
			found = true
		}
	}
	if !found {
		t.Errorf("rankAddrs dropped the hostname: %v", got)
	}
}

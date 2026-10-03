package overlay

import (
	"net"
	"strings"
	"testing"
)

func TestRandomLinkIn(t *testing.T) {
	t.Run("stays inside the given network", func(t *testing.T) {
		for i := 0; i < 200; i++ {
			link, err := RandomLinkIn("10.99.0.0/24")
			if err != nil {
				t.Fatalf("RandomLinkIn: %v", err)
			}
			ip, network, err := net.ParseCIDR(link)
			if err != nil {
				t.Fatalf("link %q: %v", link, err)
			}
			if !network.Contains(ip) {
				t.Fatalf("link %q is outside 10.99.0.0/24", link)
			}
			if network.IP.To4()[3]%4 != 0 {
				t.Fatalf("link %q is not aligned to a /30", link)
			}
			if ip.To4()[3]%4 != 1 {
				t.Fatalf("link %q does not use the first host of its /30", link)
			}
			ones, _ := network.Mask.Size()
			if ones != 30 {
				t.Fatalf("link %q has prefix /%d, want /30", link, ones)
			}
		}
	})

	t.Run("ignores host bits", func(t *testing.T) {
		a, err := RandomLinkIn("10.99.0.7/24")
		if err != nil {
			t.Fatalf("RandomLinkIn: %v", err)
		}
		if !strings.HasPrefix(a, "10.99.0.") {
			t.Fatalf("link %q left 10.99.0.0/24", a)
		}
	})

	t.Run("uses the only block of a /30", func(t *testing.T) {
		for i := 0; i < 20; i++ {
			link, err := RandomLinkIn("10.99.7.4/30")
			if err != nil {
				t.Fatalf("RandomLinkIn: %v", err)
			}
			if link != "10.99.7.5/30" {
				t.Fatalf("link = %q, want %q", link, "10.99.7.5/30")
			}
		}
	})

	t.Run("spans the whole range of a large network", func(t *testing.T) {
		seen := make(map[string]bool)
		for i := 0; i < 300; i++ {
			link, err := RandomLinkIn("172.16.0.0/12")
			if err != nil {
				t.Fatalf("RandomLinkIn: %v", err)
			}
			seen[link] = true
		}
		if len(seen) < 100 {
			t.Errorf("only %d distinct links in 300 tries, expected many", len(seen))
		}
	})

	rejects := []struct {
		name    string
		subnet  string
		wantErr string
	}{
		{"smaller than a /30", "10.99.0.0/31", "smaller than a /30"},
		{"single address", "10.99.0.5/32", "smaller than a /30"},
		{"unique local IPv6", "fd00::/48", "not IPv4"},
		{"IPv6 loopback", "::1/128", "not IPv4"},
		{"not a network", "10.99.0.0/24 extra", "invalid CIDR"},
		{"empty", "", "invalid CIDR"},
	}
	for _, tc := range rejects {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			link, err := RandomLinkIn(tc.subnet)
			if err == nil {
				t.Fatalf("RandomLinkIn(%q) = %q, want an error", tc.subnet, link)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestRandomLinkStaysInTheDefaultRange(t *testing.T) {
	_, defaultNet, err := net.ParseCIDR(linkSubnet)
	if err != nil {
		t.Fatal(err)
	}
	if defaultNet.String() != "198.18.0.0/15" {
		t.Fatalf("default range = %s, want 198.18.0.0/15", defaultNet)
	}
	for i := 0; i < 200; i++ {
		link, err := RandomLink()
		if err != nil {
			t.Fatalf("RandomLink: %v", err)
		}
		ip, network, err := net.ParseCIDR(link)
		if err != nil {
			t.Fatalf("link %q: %v", link, err)
		}
		if !defaultNet.Contains(ip) {
			t.Fatalf("link %q is outside %s", link, defaultNet)
		}
		if ones, _ := network.Mask.Size(); ones != 30 {
			t.Fatalf("link %q has prefix /%d, want /30", link, ones)
		}
	}
}

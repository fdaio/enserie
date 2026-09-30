package overlay

import (
	"fmt"
	"net"
)

// Device is a layer-3 packet source and sink.
// A real TUN implements this. Tests inject a memory device.
type Device interface {
	Name() string
	ReadPacket(b []byte) (int, error)
	WritePacket(b []byte) (int, error)
	Close() error
}

func parseIPv4(cidr string) (net.IP, *net.IPNet, error) {
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, nil, err
	}
	v4 := ip.To4()
	if v4 == nil {
		return nil, nil, fmt.Errorf("overlay requires IPv4, got %s", cidr)
	}
	ones, bits := n.Mask.Size()
	if bits != 32 {
		return nil, nil, fmt.Errorf("overlay requires IPv4, got %s", cidr)
	}
	if ones > 30 {
		return nil, nil, fmt.Errorf("prefix must be /30 or larger host space, got /%d", ones)
	}
	return v4, n, nil
}

func parseIPv4Host(s string) (net.IP, error) {
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP %q", s)
	}
	v4 := ip.To4()
	if v4 == nil {
		return nil, fmt.Errorf("overlay requires IPv4, got %s", s)
	}
	return v4, nil
}

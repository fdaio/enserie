package overlay

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

const inviteVersion = 1

// DefaultRelayURLs are tyd's hosted splices. The protocol matches tyd.
func DefaultRelayURLs() []string {
	return []string{
		"https://relay-1.getfda.dev",
		"https://relay-2.getfda.dev",
	}
}

// Invite is the copy-paste token from `ens invite`.
type Invite struct {
	V      int      `json:"v"`
	ID     string   `json:"id"`
	FP     string   `json:"fp"`
	CIDR   string   `json:"cidr"`
	Addrs  []string `json:"addrs,omitempty"`
	Relays []string `json:"relays,omitempty"`
	Secret string   `json:"s"`
}

func (inv Invite) Encode() (string, error) {
	if inv.V == 0 {
		inv.V = inviteVersion
	}
	b, err := json.Marshal(inv)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func ParseInvite(token string) (Invite, error) {
	token = strings.TrimSpace(token)
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return Invite{}, fmt.Errorf("invite token: %w", err)
	}
	var inv Invite
	if err := json.Unmarshal(raw, &inv); err != nil {
		return Invite{}, fmt.Errorf("invite token: %w", err)
	}
	if inv.V != inviteVersion {
		return Invite{}, fmt.Errorf("invite version %d is not supported", inv.V)
	}
	if inv.ID == "" || inv.FP == "" || inv.CIDR == "" || inv.Secret == "" {
		return Invite{}, fmt.Errorf("invite token is missing fields")
	}
	if len(inv.Relays) == 0 {
		inv.Relays = DefaultRelayURLs()
	}
	return inv, nil
}

// OtherCIDR returns the other host in the same IPv4 prefix.
func OtherCIDR(cidr string) (string, error) {
	local, n, err := parseIPv4(cidr)
	if err != nil {
		return "", err
	}
	peer, err := otherUsable(local, n)
	if err != nil {
		return "", err
	}
	ones, _ := n.Mask.Size()
	return fmt.Sprintf("%s/%d", peer, ones), nil
}

func otherUsable(local net.IP, n *net.IPNet) (net.IP, error) {
	ones, bits := n.Mask.Size()
	if bits != 32 {
		return nil, fmt.Errorf("overlay requires IPv4")
	}
	start := binary.BigEndian.Uint32(n.IP.To4())
	hosts := uint32(1) << uint(32-ones)
	end := start + hosts - 1
	want := local.To4()
	for x := start + 1; x < end; x++ {
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, x)
		if !ip.Equal(want) {
			return ip, nil
		}
	}
	return nil, fmt.Errorf("no other host in %s", n)
}

func FilterOverlayAddrs(addrs []string, n *net.IPNet) []string {
	if n == nil {
		return addrs
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		host, _, err := net.SplitHostPort(a)
		if err != nil {
			out = append(out, a)
			continue
		}
		ip := net.ParseIP(host)
		if ip != nil && n.Contains(ip) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// RandomLink picks a /30 in 198.18.0.0/15 (RFC 2544). That range is not
// used on most LANs, so the TUN route is not hidden by a 10.0.0.0/8.
func RandomLink() (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	x := 18 + int(b[0])%2
	y := int(b[1])
	z := int(b[2]) &^ 3
	return fmt.Sprintf("198.%d.%d.%d/30", x, y, z+1), nil
}

func RandomID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func RandomSecret() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

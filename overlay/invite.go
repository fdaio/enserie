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
	"time"
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
	// V is the token format version. Encode fills it when it is empty.
	V int `json:"v"`
	// Exp is when the token stops being valid, in Unix seconds. Encode fills
	// it with now plus InviteTTL when it is empty, so a token does not stay
	// usable long after the operator stopped waiting for the second machine.
	Exp int64 `json:"exp,omitempty"`
	// ID is the inviting node id.
	ID string `json:"id"`
	// FP is the inviting node certificate fingerprint in hex.
	FP string `json:"fp"`
	// CIDR is the inviting node overlay address, e.g. 10.7.0.1/30.
	CIDR string `json:"cidr"`
	// Addrs are the QUIC candidates that the inviting node can be dialed on.
	Addrs []string `json:"addrs,omitempty"`
	// Relays are the relays that both nodes use. ParseInvite fills the
	// hosted splices when the token omits them.
	Relays []string `json:"relays,omitempty"`
	// Secret is the shared secret that authenticates the accepting node.
	Secret string `json:"s"`
}

// InviteTTL is how long a token stays valid when the caller sets no deadline
// of its own. Pairing means pasting a token by hand, so this only has to
// outlast an operator who is still at the other machine.
const InviteTTL = 30 * time.Minute

// Encode returns the token to hand to the accepting node. It fills the version
// and the expiry when they are empty.
func (inv Invite) Encode() (string, error) {
	if inv.V == 0 {
		inv.V = inviteVersion
	}
	if inv.Exp == 0 {
		inv.Exp = time.Now().Add(InviteTTL).Unix()
	}
	b, err := json.Marshal(inv)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ParseInvite reads a token. It rejects an unknown version, a missing field,
// and a token past its expiry, and it fills Relays with the hosted splices
// when the token omits them.
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
	// A token without a deadline cannot be aged out, so refuse it rather than
	// accept a credential of unknown age.
	if inv.Exp == 0 {
		return Invite{}, fmt.Errorf("invite token has no expiry")
	}
	if time.Now().After(time.Unix(inv.Exp, 0)) {
		return Invite{}, fmt.Errorf("invite token expired at %s", time.Unix(inv.Exp, 0).UTC().Format(time.RFC3339))
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

// FilterOverlayAddrs drops the addresses inside the overlay prefix, because a
// peer cannot reach those addresses from outside the link.
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

// linkBlocks is how many /30 links fit in 198.18.0.0/15, which is the range
// RFC 2544 reserves for benchmarks. That range is not used on most LANs, so the
// TUN route is not hidden by a 10.0.0.0/8.
const linkSubnet = "198.18.0.0/15"

// RandomLink picks a /30 in 198.18.0.0/15 (RFC 2544).
func RandomLink() (string, error) {
	return RandomLinkIn(linkSubnet)
}

// RandomLinkIn picks a /30 inside the given IPv4 network, for callers whose
// default range is taken by a VPN or another network. Only the network part of
// subnet counts, so both 10.99.0.0/24 and 10.99.0.5/24 give the same range. The
// network must hold at least one /30.
func RandomLinkIn(subnet string) (string, error) {
	ip, network, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", fmt.Errorf("overlay subnet: %w", err)
	}
	v4 := ip.To4()
	if v4 == nil {
		return "", fmt.Errorf("overlay subnet %q is not IPv4", subnet)
	}
	ones, bits := network.Mask.Size()
	if bits != 32 {
		return "", fmt.Errorf("overlay subnet %q is not IPv4", subnet)
	}
	if ones > 30 {
		return "", fmt.Errorf("overlay subnet %q is smaller than a /30", subnet)
	}
	blocks := 1 << (32 - ones - 2)
	var pick [4]byte
	if _, err := rand.Read(pick[:]); err != nil {
		return "", err
	}
	base := network.IP.To4()
	offset := (uint32(pick[0])<<24 | uint32(pick[1])<<16 | uint32(pick[2])<<8 | uint32(pick[3])) % uint32(blocks)
	value := binary.BigEndian.Uint32(base) + offset*4
	local := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(local, value+1)
	return fmt.Sprintf("%s/30", local), nil
}

// RandomID returns a random node id for one side of a link.
func RandomID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// RandomSecret returns a random invite secret for one link.
func RandomSecret() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

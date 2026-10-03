// Package relay is a blind byte-pipe rendezvous for dual-NAT peers.
// Control messages use length-prefixed JSON; after a successful dial/accept
// handshake the connection becomes a raw bidirectional pipe.
package relay

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// MaxMsg caps one control message. ReadMsg reads the length before the body,
// so the cap keeps a wrong length from reserving memory for it.
const MaxMsg = 64 << 10

const (
	// TypeOffer registers a long-lived rendezvous for DaemonID.
	TypeOffer = "offer"
	// TypeDial asks the relay for a peer and waits for a ticket.
	TypeDial = "dial"
	// TypeAccept claims the ticket of a waiting dial.
	TypeAccept = "accept"
	// TypeIncoming hands a ticket to the node that offered.
	TypeIncoming = "incoming"
	// TypeOK acknowledges an offer, a dial, or an accept.
	TypeOK = "ok"
	// TypeError reports a failure and puts the reason in Error.
	TypeError = "error"
)

// Msg is one control message. The relay reads the type and uses the fields
// that the type needs.
type Msg struct {
	// Type is one of the Type constants.
	Type string `json:"type"`
	// DaemonID names the node that offers a rendezvous.
	DaemonID string `json:"daemon_id,omitempty"`
	// PeerID names the node that a dial asks for.
	PeerID string `json:"peer_id,omitempty"`
	// Ticket identifies one waiting dial.
	Ticket string `json:"ticket,omitempty"`
	// Error carries the reason of a TypeError message.
	Error string `json:"error,omitempty"`
	// Observed is the address that the relay saw for the other end. It is
	// empty when the relay cannot determine one.
	Observed string `json:"observed,omitempty"`
}

// WriteMsg writes one control message with a length prefix.
func WriteMsg(w io.Writer, m Msg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(b) > MaxMsg {
		return fmt.Errorf("relay message too large")
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// ReadMsg reads one control message. It reports a readable error when the peer
// answered with an HTTP page instead of opening a WebSocket.
func ReadMsg(r io.Reader) (Msg, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Msg{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	// 0x48545450 == "HTTP" — edge returned an HTTP page instead of WebSocket.
	if n == 0x48545450 {
		return Msg{}, fmt.Errorf("relay: got HTTP response (need WebSocket; is the relay up behind TLS?)")
	}
	if n == 0 || n > MaxMsg {
		return Msg{}, fmt.Errorf("relay message length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return Msg{}, err
	}
	var m Msg
	if err := json.Unmarshal(buf, &m); err != nil {
		return Msg{}, err
	}
	return m, nil
}

// WebSocketURL turns a relay URL into a ws/wss URL for dialing.
func WebSocketURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "off" {
		return "", fmt.Errorf("relay disabled")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("relay URL missing host: %q", raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "wss":
		u.Scheme = "wss"
	case "http", "ws":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported relay scheme %q", u.Scheme)
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

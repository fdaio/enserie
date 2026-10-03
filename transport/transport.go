// Package transport carries the overlay data plane over QUIC or a relay.
//
// Both paths run inside TLS 1.3, and a peer is accepted by certificate
// fingerprint instead of by a certificate authority. Wrap adds the topology
// metadata that the overlay logs.
package transport

import (
	"crypto/tls"
	"fmt"
	"net"
)

// Kind names the transport that carries a peer connection.
type Kind string

const (
	// KindQUIC is a direct QUIC connection.
	KindQUIC Kind = "quic"
	// KindRelay is a connection that a relay spliced.
	KindRelay Kind = "relay"
)

// Endpoint identifies how to listen or dial.
type Endpoint struct {
	// Kind is the transport. An empty kind leaves the address plain.
	Kind Kind
	// Address is a host:port pair.
	Address string
}

// String returns the address, prefixed with the kind when there is one.
func (e Endpoint) String() string {
	if e.Kind == "" {
		return e.Address
	}
	return string(e.Kind) + "://" + e.Address
}

// Conn is a transport connection with topology metadata.
type Conn interface {
	net.Conn
	Info() Info
}

// Info describes the topology of a connection.
type Info struct {
	// Transport names the path.
	Transport Kind `json:"transport"`
	// LocalAddr is this end of the connection. Wrap fills it when empty.
	LocalAddr string `json:"local_addr"`
	// RemoteAddr is the peer end. Wrap fills it when empty.
	RemoteAddr string `json:"remote_addr"`
	// TLS marks a connection that this package secures with TLS.
	TLS bool `json:"tls"`
	// CertFP is the peer fingerprint. It stays empty when no peer
	// fingerprint was checked.
	CertFP string `json:"cert_fp,omitempty"`
}

type wrapped struct {
	net.Conn
	info Info
}

func (w *wrapped) Info() Info { return w.info }

// Wrap returns c with topology metadata. It fills the two addresses from the
// connection when the caller left them empty.
func Wrap(c net.Conn, info Info) Conn {
	if info.LocalAddr == "" && c.LocalAddr() != nil {
		info.LocalAddr = c.LocalAddr().String()
	}
	if info.RemoteAddr == "" && c.RemoteAddr() != nil {
		info.RemoteAddr = c.RemoteAddr().String()
	}
	return &wrapped{Conn: c, info: info}
}

// ChannelBinder returns the TLS exporter for this connection, or an error.
// Overlay identity uses certificate fingerprints; the exporter is kept so a
// caller can bind an application handshake to this session.
func ChannelBinder(conn net.Conn) ([]byte, error) {
	switch c := conn.(type) {
	case *wrapped:
		return ChannelBinder(c.Conn)
	case *tls.Conn:
		state := c.ConnectionState()
		return Binder(&state)
	case *quicStreamConn:
		state := c.sess.ConnectionState()
		return Binder(&state.TLS)
	}
	return nil, fmt.Errorf("no channel binding for a %T connection", conn)
}

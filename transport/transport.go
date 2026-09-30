package transport

import (
	"crypto/tls"
	"fmt"
	"net"
)

type Kind string

const (
	KindQUIC  Kind = "quic"
	KindRelay Kind = "relay"
)

// Endpoint identifies how to listen or dial.
type Endpoint struct {
	Kind    Kind
	Address string
}

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

type Info struct {
	Transport  Kind   `json:"transport"`
	LocalAddr  string `json:"local_addr"`
	RemoteAddr string `json:"remote_addr"`
	TLS        bool   `json:"tls"`
	CertFP     string `json:"cert_fp,omitempty"`
}

type wrapped struct {
	net.Conn
	info Info
}

func (w *wrapped) Info() Info { return w.info }

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

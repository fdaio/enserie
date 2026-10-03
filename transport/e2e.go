package transport

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"time"
)

// Inner TLS for the relay path.
//
// The relay splices two WebSocket legs with io.Copy and never looks inside.
// Both peers wrap their own leg in TLS right after the splice so the relay
// copies ciphertext it cannot read. Each side pins the peer certificate
// fingerprint from overlay config.

// E2EHandshakeTimeout bounds the TLS handshake on a spliced connection.
const E2EHandshakeTimeout = 30 * time.Second

// E2EServerConfig returns the TLS config for the accepting side of a splice.
// allowFP decides which peer fingerprint is accepted.
func E2EServerConfig(cert tls.Certificate, allowFP func(string) bool) *tls.Config {
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequireAnyClientCert,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return verifyAllowedFP(rawCerts, allowFP)
		},
	}
	return cfg
}

// E2EClientConfig returns the TLS config for the dialing side of a splice. It
// pins the server fingerprint, so it skips the certificate authority check.
func E2EClientConfig(cert tls.Certificate, serverFP string) *tls.Config {
	return &tls.Config{
		Certificates:       []tls.Certificate{cert},
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true,
		ServerName:         "enserie",
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return verifyPeerFP(rawCerts, serverFP)
		},
	}
}

// ServerE2E wraps a spliced connection in TLS as the server.
func ServerE2E(conn net.Conn, cfg *tls.Config) (*tls.Conn, error) {
	if cfg == nil {
		return nil, fmt.Errorf("relay e2e: nil server config")
	}
	tc := tls.Server(conn, cfg)
	return handshake(tc)
}

// ClientE2E wraps a spliced connection in TLS as the client.
func ClientE2E(conn net.Conn, cfg *tls.Config) (*tls.Conn, error) {
	if cfg == nil {
		return nil, fmt.Errorf("relay e2e: nil client config")
	}
	tc := tls.Client(conn, cfg)
	return handshake(tc)
}

func handshake(tc *tls.Conn) (*tls.Conn, error) {
	_ = tc.SetDeadline(time.Now().Add(E2EHandshakeTimeout))
	if err := tc.Handshake(); err != nil {
		_ = tc.Close()
		return nil, fmt.Errorf("relay e2e handshake: %w", err)
	}
	_ = tc.SetDeadline(time.Time{})
	return tc, nil
}

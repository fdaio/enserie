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

const E2EHandshakeTimeout = 30 * time.Second

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

func ServerE2E(conn net.Conn, cfg *tls.Config) (*tls.Conn, error) {
	if cfg == nil {
		return nil, fmt.Errorf("relay e2e: nil server config")
	}
	tc := tls.Server(conn, cfg)
	return handshake(tc)
}

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

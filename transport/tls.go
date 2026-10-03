package transport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Fingerprint returns the SHA-256 of a certificate in hex.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// ShortFP shortens a fingerprint for logs.
func ShortFP(fp string) string {
	if len(fp) > 16 {
		return fp[:16]
	}
	return fp
}

// NormalizeFP lowercases a fingerprint and trims spaces, so a value that a user
// pasted compares equal to the value that the code computes.
func NormalizeFP(fp string) string {
	return strings.ToLower(strings.TrimSpace(fp))
}

// EnsureServerCert loads the certificate pair, or creates a self-signed pair
// when the files are missing. Peers pin a certificate by fingerprint, so the
// names in the certificate are never checked.
func EnsureServerCert(certPath, keyPath string) (tls.Certificate, error) {
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return tls.LoadX509KeyPair(certPath, keyPath)
		}
	}
	return generateAndWriteCert(certPath, keyPath)
}

// CertFingerprint returns the fingerprint of a certificate in hex.
func CertFingerprint(cert tls.Certificate) (string, error) {
	if len(cert.Certificate) == 0 {
		return "", fmt.Errorf("empty certificate")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return "", err
	}
	return Fingerprint(leaf), nil
}

func generateAndWriteCert(certPath, keyPath string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "enserie"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:     []string{"localhost", "enserie"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return tls.Certificate{}, err
	}
	certOut, err := os.OpenFile(certPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		_ = certOut.Close()
		return tls.Certificate{}, err
	}
	_ = certOut.Close()

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyOut, err := os.OpenFile(keyPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		_ = keyOut.Close()
		return tls.Certificate{}, err
	}
	_ = keyOut.Close()
	return tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

// LoadPinnedCert reads a PEM certificate from a file.
func LoadPinnedCert(path string) (*x509.Certificate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("no PEM certificate in %s", path)
	}
	return x509.ParseCertificate(block.Bytes)
}

func verifyPeerFP(rawCerts [][]byte, wantFP string) error {
	wantFP = NormalizeFP(wantFP)
	if wantFP == "" {
		return fmt.Errorf("empty certificate fingerprint")
	}
	if len(rawCerts) == 0 {
		return fmt.Errorf("peer presented no certificate")
	}
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return err
	}
	got := Fingerprint(cert)
	if got != wantFP {
		return fmt.Errorf("untrusted peer certificate (fp %s)", ShortFP(got))
	}
	return nil
}

func verifyAllowedFP(rawCerts [][]byte, allow func(string) bool) error {
	if allow == nil {
		return fmt.Errorf("no peer fingerprint allow list")
	}
	if len(rawCerts) == 0 {
		return fmt.Errorf("peer presented no certificate")
	}
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return err
	}
	got := Fingerprint(cert)
	if !allow(got) {
		return fmt.Errorf("untrusted peer certificate (fp %s)", ShortFP(got))
	}
	return nil
}

const bindingLabel = "enserie-e2e-v1"
const bindingSize = 32

// Binder derives a channel binding value from a completed TLS 1.3 session.
func Binder(state *tls.ConnectionState) ([]byte, error) {
	if state == nil {
		return nil, fmt.Errorf("no TLS state")
	}
	if state.Version != tls.VersionTLS13 {
		return nil, fmt.Errorf("end-to-end TLS must be 1.3, got %x", state.Version)
	}
	out, err := state.ExportKeyingMaterial(bindingLabel, nil, bindingSize)
	if err != nil {
		return nil, fmt.Errorf("channel binding: %w", err)
	}
	return out, nil
}

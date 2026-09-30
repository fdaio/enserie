package transport

import (
	"io"
	"path/filepath"
	"testing"
	"time"
)

func TestQUICRoundTripPin(t *testing.T) {
	dir := t.TempDir()
	cert, err := EnsureServerCert(filepath.Join(dir, "c.crt"), filepath.Join(dir, "c.key"))
	if err != nil {
		t.Fatal(err)
	}
	ln, fp, err := ListenQUIC("127.0.0.1:0", cert, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	errCh := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer c.Close()
		if _, err := c.Write([]byte("pong")); err != nil {
			errCh <- err
			return
		}
		buf := make([]byte, 4)
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := io.ReadFull(c, buf); err != nil {
			errCh <- err
			return
		}
		if string(buf) != "ping" {
			errCh <- errString("got " + string(buf))
			return
		}
		errCh <- nil
	}()

	c, err := DialQUICFingerprint(ln.Addr().String(), fp)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(buf) != "pong" {
		t.Fatalf("got %q", buf)
	}
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestDialQUICRejectsWrongFingerprint(t *testing.T) {
	dir := t.TempDir()
	cert, err := EnsureServerCert(filepath.Join(dir, "c.crt"), filepath.Join(dir, "c.key"))
	if err != nil {
		t.Fatal(err)
	}
	ln, _, err := ListenQUIC("127.0.0.1:0", cert, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, err = DialQUICFingerprint(ln.Addr().String(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err == nil {
		t.Fatal("expected fingerprint mismatch")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

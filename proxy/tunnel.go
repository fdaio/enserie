package proxy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/hashicorp/yamux"
)

func (s *Server) serveStream(ctx context.Context, stream *yamux.Stream) {
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(handshakeTimeout))
	target, err := readTarget(stream)
	if err != nil {
		s.logf("proxy: target: %v", err)
		_ = writeStatus(stream, repGeneralFailure)
		return
	}
	target = s.mapTarget(target)

	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		s.logf("proxy: dial %q: %v", target, err)
		_ = writeStatus(stream, replyForDialError(err))
		return
	}
	defer conn.Close()
	if err := writeStatus(stream, repSuccess); err != nil {
		return
	}
	_ = stream.SetDeadline(time.Time{})
	// A shutdown must unblock both copies. Closing the stream alone leaves the
	// target read waiting for data that may never come.
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
			_ = stream.Close()
		case <-done:
		}
	}()
	pipe(conn, stream)
	close(done)
}

// writeTarget sends the target as a length-prefixed string. The peer reads it
// before any payload, so one stream carries exactly one connection.
func writeTarget(w io.Writer, target string) error {
	if len(target) == 0 || len(target) > maxTarget {
		return fmt.Errorf("target length %d", len(target))
	}
	buf := make([]byte, 2+len(target))
	binary.BigEndian.PutUint16(buf[:2], uint16(len(target)))
	copy(buf[2:], target)
	_, err := w.Write(buf)
	return err
}

func readTarget(r io.Reader) (string, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return "", err
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	if n == 0 || n > maxTarget {
		return "", fmt.Errorf("target length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	target := string(buf)
	if _, _, err := net.SplitHostPort(target); err != nil {
		return "", fmt.Errorf("target %q: %w", target, err)
	}
	return target, nil
}

func writeStatus(w io.Writer, status byte) error {
	_, err := w.Write([]byte{status})
	return err
}

func readStatus(r io.Reader) (byte, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return repGeneralFailure, err
	}
	return b[0], nil
}

func replyForDialError(err error) byte {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return repConnectionRefused
	}
	return repHostUnreachable
}

// pipe copies in both directions and half-closes the destination when one
// direction ends. yamux Stream.Close and TCP CloseWrite both keep the other
// direction readable, so a protocol that answers after its request is done
// still delivers the answer.
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		closeWrite(a)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		closeWrite(b)
	}()
	wg.Wait()
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}

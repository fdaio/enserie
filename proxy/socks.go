package proxy

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

const (
	socksVersion = 5

	socksNoAuth = 0

	socksCmdConnect = 1

	repSuccess              = 0
	repGeneralFailure       = 1
	repNetworkUnreachable   = 3
	repHostUnreachable      = 4
	repConnectionRefused    = 5
	repCommandNotSupported  = 7
	repAddrTypeNotSupported = 8
)

func (s *Server) serveSOCKS(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))
	ok, err := readGreeting(conn)
	if err != nil {
		s.logf("socks: greeting: %v", err)
		return
	}
	if !ok {
		_ = writeMethod(conn, 0xff)
		return
	}
	if err := writeMethod(conn, socksNoAuth); err != nil {
		return
	}
	target, rep, err := readRequest(conn)
	if err != nil {
		s.logf("socks: request: %v", err)
		_ = writeReply(conn, rep)
		return
	}
	if rep != repSuccess {
		_ = writeReply(conn, rep)
		return
	}

	sess := s.waitSession(2 * time.Second)
	if sess == nil {
		_ = writeReply(conn, repNetworkUnreachable)
		return
	}
	stream, err := sess.OpenStream()
	if err != nil {
		s.logf("socks: open stream: %v", err)
		_ = writeReply(conn, repNetworkUnreachable)
		return
	}
	defer stream.Close()

	_ = stream.SetDeadline(time.Now().Add(handshakeTimeout))
	if err := writeTarget(stream, target); err != nil {
		s.logf("socks: target: %v", err)
		_ = writeReply(conn, repGeneralFailure)
		return
	}
	status, err := readStatus(stream)
	if err != nil {
		s.logf("socks: status: %v", err)
		_ = writeReply(conn, repHostUnreachable)
		return
	}
	if status != repSuccess {
		_ = writeReply(conn, status)
		return
	}
	if err := writeReply(conn, repSuccess); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	_ = stream.SetDeadline(time.Time{})
	pipe(conn, stream)
}

// readGreeting reads the SOCKS5 method list. It reports false when the client
// offers no method this server accepts.
func readGreeting(r io.Reader) (bool, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return false, err
	}
	if hdr[0] != socksVersion {
		return false, fmt.Errorf("version %d", hdr[0])
	}
	n := int(hdr[1])
	if n == 0 {
		return false, fmt.Errorf("no methods")
	}
	methods := make([]byte, n)
	if _, err := io.ReadFull(r, methods); err != nil {
		return false, err
	}
	for _, m := range methods {
		if m == socksNoAuth {
			return true, nil
		}
	}
	return false, nil
}

// readRequest reads one SOCKS5 CONNECT request. A non-zero reply code is a
// refusal the caller must send back to the client.
func readRequest(r io.Reader) (string, byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return "", repGeneralFailure, err
	}
	if hdr[0] != socksVersion {
		return "", repGeneralFailure, fmt.Errorf("version %d", hdr[0])
	}
	if hdr[2] != 0 {
		return "", repGeneralFailure, fmt.Errorf("reserved byte %d", hdr[2])
	}
	if hdr[1] != socksCmdConnect {
		return "", repCommandNotSupported, nil
	}

	var host string
	switch hdr[3] {
	case 1:
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", repGeneralFailure, err
		}
		host = net.IP(b[:]).String()
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(r, n[:]); err != nil {
			return "", repGeneralFailure, err
		}
		if n[0] == 0 {
			return "", repGeneralFailure, fmt.Errorf("empty domain")
		}
		b := make([]byte, int(n[0]))
		if _, err := io.ReadFull(r, b); err != nil {
			return "", repGeneralFailure, err
		}
		host = string(b)
	case 4:
		var b [16]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", repGeneralFailure, err
		}
		host = net.IP(b[:]).String()
	default:
		return "", repAddrTypeNotSupported, nil
	}

	var port [2]byte
	if _, err := io.ReadFull(r, port[:]); err != nil {
		return "", repGeneralFailure, err
	}
	n := binary.BigEndian.Uint16(port[:])
	if n == 0 {
		return "", repGeneralFailure, fmt.Errorf("port 0")
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(n)))
	if len(target) > maxTarget {
		return "", repGeneralFailure, fmt.Errorf("target too long")
	}
	return target, repSuccess, nil
}

func writeMethod(w io.Writer, method byte) error {
	_, err := w.Write([]byte{socksVersion, method})
	return err
}

func writeReply(w io.Writer, rep byte) error {
	_, err := w.Write([]byte{socksVersion, rep, 0, 1, 0, 0, 0, 0, 0, 0})
	return err
}

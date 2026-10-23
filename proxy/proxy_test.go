package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestReadGreetingAcceptsNoAuth(t *testing.T) {
	ok, err := readGreeting(bytes.NewReader([]byte{5, 1, 0}))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	ok, err = readGreeting(bytes.NewReader([]byte{5, 1, 2}))
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v, want no accepted method", ok, err)
	}
	if _, err := readGreeting(bytes.NewReader([]byte{4, 1, 0})); err == nil {
		t.Fatal("accepted a SOCKS4 greeting")
	}
}

func TestReadRequestTargets(t *testing.T) {
	tests := []struct {
		name string
		req  []byte
		want string
	}{
		{
			name: "ipv4",
			req:  []byte{5, 1, 0, 1, 127, 0, 0, 1, 0x1f, 0x90},
			want: "127.0.0.1:8080",
		},
		{
			name: "domain",
			req:  append([]byte{5, 1, 0, 3, 11}, append([]byte("example.com"), 0x01, 0xbb)...),
			want: "example.com:443",
		},
		{
			name: "ipv6",
			req:  append([]byte{5, 1, 0, 4}, append(net.ParseIP("::1").To16(), 0x00, 0x50)...),
			want: "[::1]:80",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, rep, err := readRequest(bytes.NewReader(tt.req))
			if err != nil || rep != repSuccess {
				t.Fatalf("rep=%d err=%v", rep, err)
			}
			if got != tt.want {
				t.Fatalf("target = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadRequestRefusesUnsupportedCommand(t *testing.T) {
	_, rep, err := readRequest(bytes.NewReader([]byte{5, 2, 0, 1, 127, 0, 0, 1, 0, 80}))
	if err != nil || rep != repCommandNotSupported {
		t.Fatalf("rep=%d err=%v", rep, err)
	}
}

func TestReadRequestRefusesUnknownAddressType(t *testing.T) {
	_, rep, err := readRequest(bytes.NewReader([]byte{5, 1, 0, 9}))
	if err != nil || rep != repAddrTypeNotSupported {
		t.Fatalf("rep=%d err=%v", rep, err)
	}
}

// The SOCKS5 listener and the yamux tunnel are separate layers. This test
// joins them: a client asks the local proxy for a target, the target string
// crosses a yamux stream, and the peer dials the echo server. The target names
// the peer's overlay address, which must map to loopback because proxy mode has
// no TUN.
func TestProxyCarriesTCPOverYamux(t *testing.T) {
	echoAddr := startEcho(t)
	_, echoPort, err := net.SplitHostPort(echoAddr)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	client := New("127.0.0.1:0", t.Logf)
	server := New("127.0.0.1:0", t.Logf)
	server.LocalIP = net.ParseIP("10.7.0.2")
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	defer server.Close()

	go func() { _ = client.HandlePeer(ctx, a, false) }()
	go func() { _ = server.HandlePeer(ctx, b, true) }()

	waitSession(t, client)
	waitSession(t, server)

	conn, err := socksDial(client.Addr().String(), net.JoinHostPort("10.7.0.2", echoPort))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 5)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("echo = %q", got)
	}
}

func TestMapTargetOnlyMapsTheLocalOverlayIP(t *testing.T) {
	s := New("", nil)
	s.LocalIP = net.ParseIP("10.7.0.2")
	if got := s.mapTarget("10.7.0.2:22"); got != "127.0.0.1:22" {
		t.Fatalf("got %q", got)
	}
	if got := s.mapTarget("10.7.0.3:22"); got != "10.7.0.3:22" {
		t.Fatalf("got %q", got)
	}
	if got := s.mapTarget("example.com:443"); got != "example.com:443" {
		t.Fatalf("got %q", got)
	}
}

func waitSession(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.session() != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("peer session did not start")
}

func startEcho(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}

func socksDial(proxyAddr, target string) (net.Conn, error) {
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		_ = c.Close()
		return nil, err
	}
	var method [2]byte
	if _, err := io.ReadFull(c, method[:]); err != nil {
		_ = c.Close()
		return nil, err
	}
	if method != [2]byte{5, 0} {
		_ = c.Close()
		return nil, fmt.Errorf("method reply %v", method)
	}

	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		_ = c.Close()
		return nil, fmt.Errorf("target %q is not IPv4", host)
	}
	req := make([]byte, 0, 10)
	req = append(req, 5, 1, 0, 1)
	req = append(req, ip...)
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := c.Write(req); err != nil {
		_ = c.Close()
		return nil, err
	}
	var reply [10]byte
	if _, err := io.ReadFull(c, reply[:]); err != nil {
		_ = c.Close()
		return nil, err
	}
	if reply[1] != repSuccess {
		_ = c.Close()
		return nil, fmt.Errorf("SOCKS reply %d", reply[1])
	}
	return c, nil
}

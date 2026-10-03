package relay

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// spliceBudget bounds every wait in these tests. A loaded CI machine must not
// turn a slow handshake into a failure, and the relay holds a dial open for 25
// seconds while it waits for the accept, so the budget covers that too.
const spliceBudget = 30 * time.Second

func TestWebSocketURL(t *testing.T) {
	ws, err := WebSocketURL("https://example.test/relay")
	if err != nil || ws != "wss://example.test/relay" {
		t.Fatalf("path: %q %v", ws, err)
	}
	ws, err = WebSocketURL("http://127.0.0.1:9090")
	if err != nil || ws != "ws://127.0.0.1:9090/" {
		t.Fatalf("http: %q %v", ws, err)
	}
	if _, err := WebSocketURL("off"); err == nil {
		t.Fatal("expected error for off")
	}
}

func TestReadMsgRejectsHTTP(t *testing.T) {
	_, err := ReadMsg(strings.NewReader("HTTP/1.1 502 Bad Gateway\r\n"))
	if err == nil || !strings.Contains(err.Error(), "HTTP response") {
		t.Fatalf("got %v", err)
	}
}

func TestRelayMsgRoundTrip(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() { _ = c1.Close(); _ = c2.Close() })
	errCh := make(chan error, 1)
	go func() {
		errCh <- WriteMsg(c1, Msg{Type: TypeOK, Ticket: "abc"})
	}()
	msg, err := ReadMsg(c2)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if msg.Type != TypeOK || msg.Ticket != "abc" {
		t.Fatalf("%+v", msg)
	}
}

func TestRelaySplicesBytes(t *testing.T) {
	addr, closeFn, err := ListenAndServe("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeFn() })
	relayURL := "http://" + addr.String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	incoming := make(chan string, 1)
	go func() {
		_ = Offer(ctx, relayURL, "node-a", func(ticket, _ string) {
			incoming <- ticket
		})
	}()

	accepted := make(chan net.Conn, 1)
	go func() {
		select {
		case ticket := <-incoming:
			c, err := Accept(ctx, relayURL, ticket)
			if err != nil {
				t.Errorf("accept: %v", err)
				accepted <- nil
				return
			}
			accepted <- c
		case <-ctx.Done():
			accepted <- nil
		}
	}()

	// The relay registers the offer in the background, so the first dials can
	// fail with "peer offline". Keep trying for the whole budget instead of a
	// fixed window, and report the last reason when the budget runs out.
	var client net.Conn
	var lastErr error
	for client == nil {
		if ctx.Err() != nil {
			break
		}
		c, err := Dial(ctx, relayURL, "node-a")
		if err == nil {
			client = c
			break
		}
		lastErr = err
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	if client == nil {
		t.Fatalf("dial never succeeded within %s: %v", spliceBudget, lastErr)
	}
	defer client.Close()

	server := <-accepted
	if server == nil {
		t.Fatal("accept failed")
	}
	defer server.Close()

	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	_ = server.SetReadDeadline(time.Now().Add(spliceBudget))
	if _, err := io.ReadFull(server, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Fatalf("got %q", buf)
	}
}

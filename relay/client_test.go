package relay

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestWebsocketHTTPClientHasNoTimeout(t *testing.T) {
	c := websocketHTTPClient()
	if c.Timeout != 0 {
		t.Fatalf("Timeout %s would kill a live splice", c.Timeout)
	}
}

func TestOfferKeepAliveStillAcceptsDial(t *testing.T) {
	prev := offerKeepAliveInterval
	offerKeepAliveInterval = 20 * time.Millisecond
	t.Cleanup(func() { offerKeepAliveInterval = prev })

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
		_ = Offer(ctx, relayURL, "node-keep", func(ticket, _ string) {
			incoming <- ticket
		})
	}()

	time.Sleep(80 * time.Millisecond)

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
		case <-time.After(5 * time.Second):
			t.Error("no incoming after keepalive")
			accepted <- nil
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	var client net.Conn
	for time.Now().Before(deadline) {
		c, err := Dial(ctx, relayURL, "node-keep")
		if err == nil {
			client = c
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if client == nil {
		t.Fatal("dial failed after offer keepalive writes")
	}
	defer client.Close()
	server := <-accepted
	if server == nil {
		t.Fatal("accept failed")
	}
	defer server.Close()
}

func TestKeepOfferAliveWritesBytes(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prev := offerKeepAliveInterval
	offerKeepAliveInterval = 10 * time.Millisecond
	t.Cleanup(func() { offerKeepAliveInterval = prev })
	go keepOfferAlive(ctx, a)
	buf := make([]byte, 1)
	_ = b.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := b.Read(buf); err != nil {
		t.Fatal(err)
	}
	if buf[0] != 0 {
		t.Fatalf("got %d", buf[0])
	}
}

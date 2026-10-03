package relay

import (
	"io"
	"net"
	"testing"
	"time"
)

func (h *Hub) offerRegistered(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.offers[id]
	return ok
}

func readMsg(t *testing.T, c net.Conn, what string) Msg {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(spliceBudget))
	msg, err := ReadMsg(c)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	return msg
}

func waitOffer(t *testing.T, hub *Hub, id string) bool {
	t.Helper()
	deadline := time.Now().Add(spliceBudget)
	for time.Now().Before(deadline) {
		if hub.offerRegistered(id) {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// The offer, the dial and the accept are three connections that the hub keeps
// in step. This drives them over pipes, so the result does not depend on how
// fast the machine schedules goroutines or on a websocket handshake.
func TestHubSplicesOfferedDialedAndAcceptedNode(t *testing.T) {
	hub := NewHub()

	offerClient, offerRelay := net.Pipe()
	dialClient, dialRelay := net.Pipe()
	acceptClient, acceptRelay := net.Pipe()
	t.Cleanup(func() {
		for _, c := range []net.Conn{offerClient, offerRelay, dialClient, dialRelay, acceptClient, acceptRelay} {
			_ = c.Close()
		}
	})

	go hub.Handle(offerRelay)
	if err := WriteMsg(offerClient, Msg{Type: TypeOffer, DaemonID: "node-a"}); err != nil {
		t.Fatalf("write offer: %v", err)
	}
	if ack := readMsg(t, offerClient, "offer ack"); ack.Type != TypeOK {
		t.Fatalf("offer ack = %+v, want %q", ack, TypeOK)
	}
	// The hub publishes the offer once the ack is out. Dialing before that
	// would only prove that a dial for an unknown peer is refused.
	if !waitOffer(t, hub, "node-a") {
		t.Fatal("offer was never registered")
	}

	go hub.Handle(dialRelay)
	if err := WriteMsg(dialClient, Msg{Type: TypeDial, PeerID: "node-a"}); err != nil {
		t.Fatalf("write dial: %v", err)
	}
	incoming := readMsg(t, offerClient, "ticket on the offer connection")
	if incoming.Type != TypeIncoming || incoming.Ticket == "" {
		t.Fatalf("offer connection got %+v, want a ticket", incoming)
	}

	go hub.Handle(acceptRelay)
	if err := WriteMsg(acceptClient, Msg{Type: TypeAccept, Ticket: incoming.Ticket}); err != nil {
		t.Fatalf("write accept: %v", err)
	}
	if ack := readMsg(t, dialClient, "ack for the dialer"); ack.Type != TypeOK {
		t.Fatalf("dialer ack = %+v, want %q", ack, TypeOK)
	}
	if ack := readMsg(t, acceptClient, "ack for the acceptor"); ack.Type != TypeOK {
		t.Fatalf("acceptor ack = %+v, want %q", ack, TypeOK)
	}

	// Both legs carry bytes once the splice is up. Nothing else reads these
	// two connections, so the payload cannot be taken by another goroutine.
	_ = acceptClient.SetReadDeadline(time.Now().Add(spliceBudget))
	_ = dialClient.SetReadDeadline(time.Now().Add(spliceBudget))

	go func() { _, _ = dialClient.Write([]byte("hello")) }()
	buf := make([]byte, 5)
	if _, err := io.ReadFull(acceptClient, buf); err != nil {
		t.Fatalf("read through the splice: %v", err)
	}
	if string(buf) != "hello" {
		t.Errorf("peer read %q, want %q", buf, "hello")
	}

	go func() { _, _ = acceptClient.Write([]byte("world")) }()
	back := make([]byte, 5)
	if _, err := io.ReadFull(dialClient, back); err != nil {
		t.Fatalf("read back through the splice: %v", err)
	}
	if string(back) != "world" {
		t.Errorf("peer read %q, want %q", back, "world")
	}
}

// A dial for a node that never offered must fail instead of waiting.
func TestHubRejectsDialForUnknownPeer(t *testing.T) {
	hub := NewHub()

	dialClient, dialRelay := net.Pipe()
	t.Cleanup(func() { _ = dialClient.Close(); _ = dialRelay.Close() })

	go hub.Handle(dialRelay)
	if err := WriteMsg(dialClient, Msg{Type: TypeDial, PeerID: "node-z"}); err != nil {
		t.Fatalf("write dial: %v", err)
	}
	reply := readMsg(t, dialClient, "reply for an unknown peer")
	if reply.Type != TypeError || reply.Error != "peer offline" {
		t.Errorf("reply = %+v, want %q with %q", reply, TypeError, "peer offline")
	}
}

// An accept for a ticket that no dial is waiting on must fail instead of
// splicing into nothing.
func TestHubRejectsAcceptForUnknownTicket(t *testing.T) {
	hub := NewHub()

	acceptClient, acceptRelay := net.Pipe()
	t.Cleanup(func() { _ = acceptClient.Close(); _ = acceptRelay.Close() })

	go hub.Handle(acceptRelay)
	if err := WriteMsg(acceptClient, Msg{Type: TypeAccept, Ticket: "nope"}); err != nil {
		t.Fatalf("write accept: %v", err)
	}
	reply := readMsg(t, acceptClient, "reply for an unknown ticket")
	if reply.Type != TypeError || reply.Error != "unknown ticket" {
		t.Errorf("reply = %+v, want %q with %q", reply, TypeError, "unknown ticket")
	}
}

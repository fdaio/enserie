package relay

import (
	"net"
	"sync"
	"testing"
	"time"
)

func (h *Hub) offerRegistered(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.offers[id]
	return ok
}

// ackGateConn holds the first write, which is the offer ack, until the test
// releases it.
type ackGateConn struct {
	net.Conn
	gate    chan struct{}
	started chan struct{}
	once    sync.Once
}

func (c *ackGateConn) Write(b []byte) (int, error) {
	wait := false
	c.once.Do(func() {
		wait = true
		close(c.started)
	})
	if wait {
		<-c.gate
	}
	return c.Conn.Write(b)
}

// The client reads its ack as the first message and tears the offer down on
// anything else. Publishing the offer before that ack leaves a window in which
// a dial delivers its ticket first, the client drops it, and the dial then
// waits out its whole accept window for a peer that never heard of it.
func TestOfferIsPublishedOnlyAfterItsAck(t *testing.T) {
	hub := NewHub()

	gate := make(chan struct{})
	started := make(chan struct{})
	offerClient, relaySide := net.Pipe()
	t.Cleanup(func() { _ = offerClient.Close(); _ = relaySide.Close() })

	go hub.Handle(&ackGateConn{Conn: relaySide, gate: gate, started: started})
	if err := WriteMsg(offerClient, Msg{Type: TypeOffer, DaemonID: "node-a"}); err != nil {
		t.Fatalf("write offer: %v", err)
	}

	<-started
	// The ack is written but held, so no dial may find this offer yet.
	time.Sleep(50 * time.Millisecond)
	if hub.offerRegistered("node-a") {
		t.Fatal("offer was published before its ack reached the client")
	}

	// The ack write still needs a reader before the hub can finish it.
	close(gate)
	_ = offerClient.SetReadDeadline(time.Now().Add(3 * time.Second))
	if ack, err := ReadMsg(offerClient); err != nil || ack.Type != TypeOK {
		t.Fatalf("ack = %+v, err = %v", ack, err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for !hub.offerRegistered("node-a") {
		if time.Now().After(deadline) {
			t.Fatal("offer was not published after its ack")
		}
		time.Sleep(time.Millisecond)
	}
}

// Once the offer is registered, a ticket must reach it.
func TestTicketReachesAnAcknowledgedOffer(t *testing.T) {
	hub := NewHub()

	offerClient, relaySide := net.Pipe()
	dialClient, dialRelay := net.Pipe()
	t.Cleanup(func() {
		for _, c := range []net.Conn{offerClient, relaySide, dialClient, dialRelay} {
			_ = c.Close()
		}
	})

	go hub.Handle(relaySide)
	if err := WriteMsg(offerClient, Msg{Type: TypeOffer, DaemonID: "node-a"}); err != nil {
		t.Fatalf("write offer: %v", err)
	}
	_ = offerClient.SetReadDeadline(time.Now().Add(3 * time.Second))
	if ack, err := ReadMsg(offerClient); err != nil || ack.Type != TypeOK {
		t.Fatalf("ack = %+v, err = %v", ack, err)
	}

	// The hub publishes the offer after the ack goes out, so wait for it.
	deadline := time.Now().Add(3 * time.Second)
	for !hub.offerRegistered("node-a") {
		if time.Now().After(deadline) {
			t.Fatal("offer was never registered")
		}
		time.Sleep(time.Millisecond)
	}

	go hub.Handle(dialRelay)
	if err := WriteMsg(dialClient, Msg{Type: TypeDial, PeerID: "node-a"}); err != nil {
		t.Fatalf("write dial: %v", err)
	}

	_ = offerClient.SetReadDeadline(time.Now().Add(3 * time.Second))
	incoming, err := ReadMsg(offerClient)
	if err != nil {
		t.Fatalf("ticket on the offer connection: %v", err)
	}
	if incoming.Type != TypeIncoming || incoming.Ticket == "" {
		t.Fatalf("offer connection got %+v, want a ticket", incoming)
	}
}

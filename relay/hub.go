package relay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type pendingDial struct {
	client   net.Conn
	acceptCh chan net.Conn
	finished chan struct{}
}

type offer struct {
	daemonID string
	control  net.Conn
	writeMu  sync.Mutex
}

// Hub matches a long-lived server offer with client dials.
type Hub struct {
	mu      sync.Mutex
	offers  map[string]*offer
	tickets map[string]*pendingDial
}

// NewHub returns a hub that holds no offer.
func NewHub() *Hub {
	return &Hub{
		offers:  make(map[string]*offer),
		tickets: make(map[string]*pendingDial),
	}
}

// Handle serves one client connection. It reads a single control message and
// acts on it, and it returns once that message has been dealt with, which for
// an offer means once the rendezvous ended.
func (h *Hub) Handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	msg, err := ReadMsg(conn)
	_ = conn.SetDeadline(time.Time{})
	if err != nil {
		return
	}
	switch msg.Type {
	case TypeOffer:
		h.handleOffer(conn, msg)
	case TypeDial:
		h.handleDial(conn, msg)
	case TypeAccept:
		h.handleAccept(conn, msg)
	default:
		_ = WriteMsg(conn, Msg{Type: TypeError, Error: "unknown type"})
	}
}

func (h *Hub) handleOffer(conn net.Conn, msg Msg) {
	id := strings.TrimSpace(msg.DaemonID)
	if id == "" {
		_ = WriteMsg(conn, Msg{Type: TypeError, Error: "daemon_id required"})
		return
	}
	o := &offer{daemonID: id, control: conn}

	// Ack first, and under the lock that the ticket write takes. The client
	// reads its ack as the first message and drops anything else, so a ticket
	// that overtakes the ack tears the offer down and the dial then waits out
	// its whole window for a peer that never heard of it.
	o.writeMu.Lock()
	err := WriteMsg(conn, Msg{Type: TypeOK})
	o.writeMu.Unlock()
	if err != nil {
		return
	}

	// Publish only after the ack is on the wire, so that no dial can pick
	// this offer up before the client knows it is registered.
	h.mu.Lock()
	if old, ok := h.offers[id]; ok {
		_ = old.control.Close()
	}
	h.offers[id] = o
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if cur, ok := h.offers[id]; ok && cur == o {
			delete(h.offers, id)
		}
		h.mu.Unlock()
	}()
	buf := make([]byte, 1)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		_, err := conn.Read(buf)
		if err != nil {
			return
		}
	}
}

func (h *Hub) handleDial(conn net.Conn, msg Msg) {
	peer := strings.TrimSpace(msg.PeerID)
	if peer == "" {
		_ = WriteMsg(conn, Msg{Type: TypeError, Error: "peer_id required"})
		return
	}
	h.mu.Lock()
	o, ok := h.offers[peer]
	h.mu.Unlock()
	if !ok {
		_ = WriteMsg(conn, Msg{Type: TypeError, Error: "peer offline"})
		return
	}
	ticket := newTicket()
	pd := &pendingDial{
		client:   conn,
		acceptCh: make(chan net.Conn, 1),
		finished: make(chan struct{}),
	}
	h.mu.Lock()
	h.tickets[ticket] = pd
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.tickets, ticket)
		h.mu.Unlock()
		close(pd.finished)
	}()

	// The offering node reconnects on its own schedule, and a new offer for
	// the same id closes the connection that this ticket would travel on. Fail
	// now so that the dialer retries at once instead of waiting out the
	// window for an accept that can no longer come.
	h.mu.Lock()
	stillOffered := h.offers[peer] == o
	h.mu.Unlock()
	if !stillOffered {
		_ = WriteMsg(conn, Msg{Type: TypeError, Error: "peer offline"})
		return
	}

	o.writeMu.Lock()
	err := WriteMsg(o.control, Msg{
		Type:     TypeIncoming,
		Ticket:   ticket,
		Observed: observedAddr(conn),
	})
	o.writeMu.Unlock()
	if err != nil {
		_ = WriteMsg(conn, Msg{Type: TypeError, Error: "peer unreachable"})
		return
	}

	var accept net.Conn
	timer := time.NewTimer(25 * time.Second)
	defer timer.Stop()
	select {
	case accept = <-pd.acceptCh:
	case <-timer.C:
		_ = WriteMsg(conn, Msg{Type: TypeError, Error: "accept timeout"})
		return
	}
	defer accept.Close()

	dialOK := Msg{Type: TypeOK, Ticket: ticket, Observed: observedAddr(o.control)}
	if err := WriteMsg(conn, dialOK); err != nil {
		return
	}
	if err := WriteMsg(accept, Msg{Type: TypeOK, Ticket: ticket}); err != nil {
		return
	}
	splice(conn, accept)
}

func observedAddr(c net.Conn) string {
	if c == nil {
		return ""
	}
	addr := c.RemoteAddr()
	if addr == nil {
		return ""
	}
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return ""
	}
	if host == "" || port == "" {
		return ""
	}
	return net.JoinHostPort(host, port)
}

func (h *Hub) handleAccept(conn net.Conn, msg Msg) {
	ticket := strings.TrimSpace(msg.Ticket)
	if ticket == "" {
		_ = WriteMsg(conn, Msg{Type: TypeError, Error: "ticket required"})
		return
	}
	h.mu.Lock()
	pd, ok := h.tickets[ticket]
	h.mu.Unlock()
	if !ok {
		_ = WriteMsg(conn, Msg{Type: TypeError, Error: "unknown ticket"})
		return
	}
	select {
	case pd.acceptCh <- conn:
		<-pd.finished
	case <-time.After(5 * time.Second):
		_ = WriteMsg(conn, Msg{Type: TypeError, Error: "dial gone"})
	}
}

var splicePingInterval = 15 * time.Second

const splicePingTimeout = 5 * time.Second

type pingConn struct {
	net.Conn
	ws *websocket.Conn
}

func splice(a, b net.Conn) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		_ = dst.Close()
		_ = src.Close()
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	go pingLegs(ctx, a, b)
	<-done
	<-done
}

func pingLegs(ctx context.Context, a, b net.Conn) {
	ticker := time.NewTicker(splicePingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := pingBoth(ctx, a, b); err != nil {
				if ctx.Err() != nil {
					return
				}
				_ = a.Close()
				_ = b.Close()
				return
			}
		}
	}
}

func pingBoth(ctx context.Context, a, b net.Conn) error {
	errc := make(chan error, 2)
	for _, c := range []net.Conn{a, b} {
		go func(c net.Conn) {
			errc <- pingLeg(ctx, c)
		}(c)
	}
	var err error
	for range 2 {
		if e := <-errc; e != nil && err == nil {
			err = e
		}
	}
	return err
}

func pingLeg(ctx context.Context, c net.Conn) error {
	pc, ok := c.(pingConn)
	if !ok || pc.ws == nil {
		return nil
	}
	pctx, cancel := context.WithTimeout(ctx, splicePingTimeout)
	defer cancel()
	return pc.ws.Ping(pctx)
}

func newTicket() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

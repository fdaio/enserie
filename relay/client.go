package relay

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

func dialRelay(ctx context.Context, relayURL string) (net.Conn, error) {
	wsURL, err := WebSocketURL(relayURL)
	if err != nil {
		return nil, err
	}
	dialCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
	}
	ws, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	})
	if err != nil {
		return nil, fmt.Errorf("relay websocket %s: %w", wsURL, err)
	}
	return websocket.NetConn(context.Background(), ws, websocket.MessageBinary), nil
}

// Offer keeps a rendezvous on the relay for nodeID. onTicket is called for
// each client dialing in; observed is the relay's view of that client's
// post-NAT address, or "" when the relay cannot determine one.
func Offer(ctx context.Context, relayURL, nodeID string, onTicket func(ticket, observed string)) error {
	if onTicket == nil {
		return fmt.Errorf("onTicket required")
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := offerOnce(ctx, relayURL, nodeID, onTicket)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func offerOnce(ctx context.Context, relayURL, nodeID string, onTicket func(ticket, observed string)) error {
	conn, err := dialRelay(ctx, relayURL)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	if err := WriteMsg(conn, Msg{Type: TypeOffer, DaemonID: nodeID}); err != nil {
		return err
	}
	ack, err := ReadMsg(conn)
	if err != nil {
		return err
	}
	if ack.Type == TypeError {
		return fmt.Errorf("relay offer: %s", ack.Error)
	}
	if ack.Type != TypeOK {
		return fmt.Errorf("relay offer: unexpected %q", ack.Type)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		msg, err := ReadMsg(conn)
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil {
			return err
		}
		if msg.Type == TypeIncoming && msg.Ticket != "" {
			onTicket(msg.Ticket, msg.Observed)
			continue
		}
		if msg.Type == TypeError {
			return fmt.Errorf("relay: %s", msg.Error)
		}
	}
}

func Accept(ctx context.Context, relayURL, ticket string) (net.Conn, error) {
	conn, err := dialRelay(ctx, relayURL)
	if err != nil {
		return nil, err
	}
	go closeOnDone(ctx, conn)
	ok := false
	defer func() {
		if !ok {
			_ = conn.Close()
		}
	}()
	if err := WriteMsg(conn, Msg{Type: TypeAccept, Ticket: ticket}); err != nil {
		return nil, err
	}
	ack, err := ReadMsg(conn)
	if err != nil {
		return nil, err
	}
	if ack.Type == TypeError {
		return nil, fmt.Errorf("relay accept: %s", ack.Error)
	}
	if ack.Type != TypeOK {
		return nil, fmt.Errorf("relay accept: unexpected %q", ack.Type)
	}
	ok = true
	return conn, nil
}

type DialResult struct {
	Conn     net.Conn
	Observed string
}

func Dial(ctx context.Context, relayURL, peerID string) (net.Conn, error) {
	res, err := DialDetailed(ctx, relayURL, peerID)
	if err != nil {
		return nil, err
	}
	return res.Conn, nil
}

func DialDetailed(ctx context.Context, relayURL, peerID string) (DialResult, error) {
	conn, err := dialRelay(ctx, relayURL)
	if err != nil {
		return DialResult{}, err
	}
	go closeOnDone(ctx, conn)
	ok := false
	defer func() {
		if !ok {
			_ = conn.Close()
		}
	}()
	if err := WriteMsg(conn, Msg{Type: TypeDial, PeerID: peerID}); err != nil {
		return DialResult{}, err
	}
	ack, err := ReadMsg(conn)
	if err != nil {
		return DialResult{}, err
	}
	if ack.Type == TypeError {
		return DialResult{}, fmt.Errorf("relay dial: %s", ack.Error)
	}
	if ack.Type != TypeOK {
		return DialResult{}, fmt.Errorf("relay dial: unexpected %q", ack.Type)
	}
	ok = true
	return DialResult{Conn: conn, Observed: ack.Observed}, nil
}

func closeOnDone(ctx context.Context, c net.Conn) {
	<-ctx.Done()
	_ = c.Close()
}

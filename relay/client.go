package relay

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

func websocketHTTPClient() *http.Client {
	// Handshake time is bound by dialCtx. Client.Timeout would also cover
	// the splice and close a live path after that duration.
	return &http.Client{}
}

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
		HTTPClient: websocketHTTPClient(),
	})
	if err != nil {
		return nil, fmt.Errorf("relay websocket %s: %w", wsURL, err)
	}
	nc := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	// Every relay leg goes through here, so the keepalive covers the offer,
	// the dial and the accept alike.
	return withKeepalive(ws, nc), nil
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
		msg, err := ReadMsg(conn)
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

// Accept claims the ticket of a waiting dial and returns the spliced
// connection.
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

// DialResult is a spliced connection and what the relay saw of the dialer.
type DialResult struct {
	// Conn is the connection to the peer.
	Conn net.Conn
	// Observed is the address that the relay saw for this end. It is empty
	// when the relay cannot determine one.
	Observed string
}

// Dial asks the relay for a peer and returns the spliced connection. It fails
// at once when the peer has no offer on this relay.
func Dial(ctx context.Context, relayURL, peerID string) (net.Conn, error) {
	res, err := DialDetailed(ctx, relayURL, peerID)
	if err != nil {
		return nil, err
	}
	return res.Conn, nil
}

// DialDetailed is Dial and also reports the address that the relay observed.
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

package overlay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/fdaio/enserie/transport"
)

// relayHedgeDelay is how long the QUIC candidates get to reach the peer on
// their own before the relay legs start. A handshake over a LAN finishes in
// milliseconds, so a candidate that can answer does so well inside this window.
// Starting the relay only after the delay keeps a direct path preferred without
// making a peer that has no direct path wait for it.
const relayHedgeDelay = 300 * time.Millisecond

// dialAttempt is one way of reaching the peer.
type dialAttempt func(ctx context.Context) (net.Conn, transport.Kind, error)

// dialResult is what one attempt produced.
type dialResult struct {
	conn net.Conn
	kind transport.Kind
	err  error
	// skipped marks a placeholder for an attempt that was never made. It keeps
	// the result count equal to the number of attempts promised to the caller
	// without reporting a failure that did not happen.
	skipped bool
}

// dialRace runs every attempt in fast at once and returns the first success.
// The attempts in slow start once hedge has passed with no winner yet.
//
// The attempts were run one after another, so a peer that advertised an
// address this node cannot answer cost the full handshake timeout per address
// before the relay was even tried. One unreachable address was five seconds of
// dropped packets, because tunLoop discards whatever it reads while no path is
// established. Racing makes the wait the slowest attempt rather than the sum of
// all of them.
func dialRace(ctx context.Context, hedge time.Duration, fast, slow []dialAttempt) (net.Conn, transport.Kind, error) {
	total := len(fast) + len(slow)
	if total == 0 {
		return nil, "", fmt.Errorf("no QUIC candidates and no relay")
	}

	dialCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The buffer holds every result, so no attempt can block on the send after
	// this function returns.
	results := make(chan dialResult, total)

	for _, a := range fast {
		go func(a dialAttempt) {
			conn, kind, err := a(dialCtx)
			results <- dialResult{conn: conn, kind: kind, err: err}
		}(a)
	}

	go func() {
		if len(fast) > 0 {
			select {
			case <-time.After(hedge):
			case <-dialCtx.Done():
				// The race is decided, so the relay is not started. Placeholders
				// stand in for its results, because the caller waits for one per
				// attempt and a goroutine that never sends would strand it.
				for range slow {
					results <- dialResult{err: context.Canceled, skipped: true}
				}
				return
			}
		}
		for _, a := range slow {
			go func(a dialAttempt) {
				conn, kind, err := a(dialCtx)
				results <- dialResult{conn: conn, kind: kind, err: err}
			}(a)
		}
	}()

	var errs []error
	for i := 0; i < total; i++ {
		select {
		case r := <-results:
			if r.err != nil {
				if !r.skipped {
					errs = append(errs, r.err)
				}
				continue
			}
			cancel()
			// Losing attempts keep running for a moment. Whatever they manage
			// to establish is closed in the background: the peer keeps the first
			// connection and refuses the rest, so a loser left open would run on
			// with no peer behind it. Draining inline would hold up the path
			// this attempt just won.
			go closeLosers(results, total-i-1)
			return r.conn, r.kind, nil
		case <-ctx.Done():
			cancel()
			go closeLosers(results, total-i-1)
			return nil, "", ctx.Err()
		}
	}
	return nil, "", errors.Join(errs...)
}

// closeLosers closes whatever the attempts that lost the race hand back.
func closeLosers(results <-chan dialResult, n int) {
	for i := 0; i < n; i++ {
		if r := <-results; r.conn != nil {
			_ = r.conn.Close()
		}
	}
}

package overlay

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/fdaio/enserie/transport"
)

// trackedConn records whether it was closed, so a test can tell an attempt that
// lost the race from one that was left running.
type trackedConn struct {
	net.Conn
	closed chan struct{}
	once   sync.Once
}

func newTrackedConn() *trackedConn {
	return &trackedConn{closed: make(chan struct{})}
}

func (c *trackedConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *trackedConn) wasClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

// settle waits for cond, so a test does not read a flag before the goroutine
// that sets it had a chance to run.
func settle(t *testing.T, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// winAfter returns an attempt that succeeds once the delay has passed.
func winAfter(delay time.Duration, conn net.Conn) dialAttempt {
	return func(ctx context.Context) (net.Conn, transport.Kind, error) {
		select {
		case <-time.After(delay):
			return conn, transport.KindQUIC, nil
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
}

// winAnyway returns an attempt that succeeds once the delay has passed whatever
// the context says. A handshake that has already completed cannot be taken back,
// which is the case that has to be closed.
func winAnyway(delay time.Duration, conn net.Conn) dialAttempt {
	return func(ctx context.Context) (net.Conn, transport.Kind, error) {
		time.Sleep(delay)
		return conn, transport.KindQUIC, nil
	}
}

// failAfter returns an attempt that fails once the delay has passed.
func failAfter(delay time.Duration) dialAttempt {
	return func(ctx context.Context) (net.Conn, transport.Kind, error) {
		select {
		case <-time.After(delay):
			return nil, "", errors.New("no recent network activity")
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
}

// An address this node cannot answer must not cost a handshake timeout of its
// own, because the attempts run at once rather than one after another.
func TestDialRaceWaitsForTheSlowestAttemptNotTheSum(t *testing.T) {
	fast := newTrackedConn()
	start := time.Now()
	conn, kind, err := dialRace(context.Background(), time.Hour,
		[]dialAttempt{failAfter(400 * time.Millisecond), failAfter(400 * time.Millisecond), winAfter(400*time.Millisecond, fast)}, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("dialRace: %v", err)
	}
	if conn != fast || kind != transport.KindQUIC {
		t.Fatalf("got %v %q", conn, kind)
	}
	// Sequentially this would have taken three times the delay. The race adds
	// only the time each attempt needed to learn it lost.
	if elapsed > 900*time.Millisecond {
		t.Errorf("took %v, which is the sum of the attempts rather than the slowest", elapsed)
	}
}

// The loser of the race must be closed. The peer keeps the first connection and
// refuses the rest, so a loser left open would run on with no peer behind it.
func TestDialRaceClosesWhatTheLosersEstablish(t *testing.T) {
	winner := newTrackedConn()
	loser := newTrackedConn()
	_, _, err := dialRace(context.Background(), time.Hour,
		[]dialAttempt{winAfter(10*time.Millisecond, winner), winAnyway(80*time.Millisecond, loser)}, nil)
	if err != nil {
		t.Fatalf("dialRace: %v", err)
	}
	if !settle(t, loser.wasClosed) {
		t.Error("the losing attempt established a connection that was never closed")
	}
	if winner.wasClosed() {
		t.Error("the winning connection was closed")
	}
}

// The relay is what a peer without a direct path needs, so it must not sit
// behind the hedge when every candidate fails immediately.
func TestDialRaceStartsTheRelayWhenNoCandidateAnswers(t *testing.T) {
	relayed := newTrackedConn()
	start := time.Now()
	conn, kind, err := dialRace(context.Background(), relayHedgeDelay,
		[]dialAttempt{failAfter(10 * time.Millisecond)},
		[]dialAttempt{winAfter(10*time.Millisecond, relayed)})
	if err != nil {
		t.Fatalf("dialRace: %v", err)
	}
	if conn != relayed || kind != transport.KindQUIC {
		t.Fatalf("got %v %q", conn, kind)
	}
	if elapsed := time.Since(start); elapsed > relayHedgeDelay+400*time.Millisecond {
		t.Errorf("took %v, the relay waited for the failed candidate instead of the hedge", elapsed)
	}
}

// A candidate that answers must win, otherwise the hedge would replace a fast
// direct path with a slower relayed one.
func TestDialRacePrefersTheCandidateThatAnswersFirst(t *testing.T) {
	direct := newTrackedConn()
	relayed := newTrackedConn()
	_, kind, err := dialRace(context.Background(), relayHedgeDelay,
		[]dialAttempt{winAfter(20*time.Millisecond, direct)},
		[]dialAttempt{winAfter(20*time.Millisecond, relayed)})
	if err != nil {
		t.Fatalf("dialRace: %v", err)
	}
	if kind != transport.KindQUIC {
		t.Errorf("got kind %q, want the direct candidate", kind)
	}
}

// The relay legs must not start while a candidate is still within the hedge,
// or a peer that pairs over the loopback would pay for a relay connection.
func TestDialRaceHoldsTheRelayInsideTheHedge(t *testing.T) {
	var started sync.WaitGroup
	started.Add(1)
	relayLeg := func(ctx context.Context) (net.Conn, transport.Kind, error) {
		started.Done()
		return newTrackedConn(), transport.KindRelay, nil
	}
	direct := newTrackedConn()
	_, _, err := dialRace(context.Background(), 5*time.Second,
		[]dialAttempt{winAfter(10*time.Millisecond, direct)},
		[]dialAttempt{relayLeg})
	if err != nil {
		t.Fatalf("dialRace: %v", err)
	}
	// The race returned long before the hedge, so the relay leg must not have
	// run yet. Wait past the hedge before checking, or the check races the timer.
	waited := make(chan struct{})
	go func() { started.Wait(); close(waited) }()
	select {
	case <-waited:
		t.Error("the relay leg started before the hedge had passed")
	case <-time.After(300 * time.Millisecond):
	}
}

// Every failure has to reach the caller, since the caller logs them and retries
// with a backoff that depends on knowing the path is unreachable.
func TestDialRaceReportsEveryFailure(t *testing.T) {
	_, _, err := dialRace(context.Background(), time.Hour,
		[]dialAttempt{failAfter(10 * time.Millisecond), failAfter(10 * time.Millisecond)}, nil)
	if err == nil {
		t.Fatal("dialRace reported success with no attempt that can succeed")
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("error %v does not carry the individual failures", err)
	}
	if len(joined.Unwrap()) != 2 {
		t.Errorf("got %d failures, want 2", len(joined.Unwrap()))
	}
}

// A peer with neither a candidate nor a relay needs the same message as before,
// because that is what says the invite carried nothing usable.
func TestDialRaceWithNothingToTry(t *testing.T) {
	_, _, err := dialRace(context.Background(), time.Hour, nil, nil)
	if err == nil {
		t.Fatal("dialRace succeeded with nothing to try")
	}
	if got := err.Error(); got != "no QUIC candidates and no relay" {
		t.Errorf("got %q", got)
	}
}

// Cancelling the caller must return rather than wait out every attempt.
func TestDialRaceStopsWhenTheCallerCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, _, err := dialRace(ctx, time.Hour,
		[]dialAttempt{failAfter(10 * time.Second), failAfter(10 * time.Second)}, nil)
	if err == nil {
		t.Fatal("dialRace ignored the cancellation")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v to notice the cancellation", elapsed)
	}
}

// The relay must stay out of the racing group so a direct path is preferred,
// and a peer pinned to the relay must get no candidates at all.
func TestDialTargetsSplitsCandidatesFromRelays(t *testing.T) {
	cfg := Config{
		Peer:   Peer{Candidates: []string{"127.0.0.1:1", "10.0.0.5:2", "127.0.0.1:1"}},
		Relays: []string{"https://relay.example", "", "off"},
	}
	addrs, relays := dialTargets(cfg)
	if len(addrs) != 2 {
		t.Errorf("got %d addresses %v, the duplicate should be dropped", len(addrs), addrs)
	}
	if addrs[0] != "10.0.0.5:2" {
		t.Errorf("first address %q, the reachable one should be tried first", addrs[0])
	}
	if addrs[1] != "127.0.0.1:1" {
		t.Errorf("second address %q, loopback stays for a peer on this machine", addrs[1])
	}
	if len(relays) != 1 || relays[0] != "https://relay.example" {
		t.Errorf("got relays %v, an empty and an off relay are not usable", relays)
	}
}

func TestDialTargetsForceRelaySkipsCandidates(t *testing.T) {
	cfg := Config{
		Peer:   Peer{ForceRelay: true, Candidates: []string{"10.0.0.5:2"}},
		Relays: []string{"https://relay.example"},
	}
	addrs, relays := dialTargets(cfg)
	if len(addrs) != 0 {
		t.Errorf("got addresses %v, a peer forced onto the relay has no direct path", addrs)
	}
	if len(relays) != 1 {
		t.Errorf("got relays %v", relays)
	}
}

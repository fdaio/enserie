package relay

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// countingListener wraps the accepted connections so a test can see the bytes a
// client sends. coder/websocket answers a ping with a pong inside its own read
// loop and exposes no hook for it, so the bytes on the wire are the only
// observable that does not depend on library internals.
type countingListener struct {
	net.Listener
	read *atomic.Int64
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: c, read: l.read}, nil
}

type countingConn struct {
	net.Conn
	read *atomic.Int64
	once sync.Once
}

func (c *countingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.read.Add(int64(n))
	}
	return n, err
}

// pingServer holds every upgraded WebSocket open. The returned counter grows by
// the number of bytes a client sends, pings included.
func pingServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var read atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close(websocket.StatusNormalClosure, "") }()
		for {
			if _, _, err := ws.Read(context.Background()); err != nil {
				return
			}
		}
	}))
	srv.Listener = &countingListener{Listener: srv.Listener, read: &read}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, &read
}

// dialTestWS returns the WebSocket and the net.Conn wrapper for it, which is
// the pair withKeepalive takes.
func dialTestWS(t *testing.T, srv *httptest.Server) (*websocket.Conn, net.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close(websocket.StatusNormalClosure, "") })
	return ws, websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
}

// An idle relay leg sends nothing of its own, and Cloudflare closes a proxied
// WebSocket after about a hundred seconds of that. The keepalive has to put a
// frame on the wire while nothing else happens.
func TestKeepaliveSendsPingsWhileIdle(t *testing.T) {
	srv, read := pingServer(t)
	ws, conn := dialTestWS(t, srv)

	oldInterval, oldWait := relayPingIntervalForTest, relayPingWaitForTest
	relayPingIntervalForTest = 50 * time.Millisecond
	relayPingWaitForTest = 50 * time.Millisecond
	defer func() {
		relayPingIntervalForTest, relayPingWaitForTest = oldInterval, oldWait
	}()

	ka := withKeepalive(ws, conn)
	t.Cleanup(func() { _ = ka.Close() })

	// The upgrade handshake is the traffic so far.
	time.Sleep(100 * time.Millisecond)
	settled := read.Load()

	// Several pings have to arrive, not one. A single frame proves the loop
	// started; repetition is what actually keeps the connection open.
	const wantPings = 3
	deadline := time.Now().Add(5 * time.Second)
	for pingsSeen(read.Load()-settled) < wantPings && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if pingsSeen(read.Load()-settled) < wantPings {
		t.Fatalf("only %d pings arrived while idle, want at least %d", pingsSeen(read.Load()-settled), wantPings)
	}
}

// Closing the wrapper has to stop the ping loop. A ticker and a goroutine that
// outlive the connection would last for the life of the process.
func TestKeepaliveStopsOnClose(t *testing.T) {
	srv, read := pingServer(t)
	ws, conn := dialTestWS(t, srv)

	oldInterval, oldWait := relayPingIntervalForTest, relayPingWaitForTest
	relayPingIntervalForTest = 40 * time.Millisecond
	relayPingWaitForTest = 40 * time.Millisecond
	defer func() {
		relayPingIntervalForTest, relayPingWaitForTest = oldInterval, oldWait
	}()

	ka := withKeepalive(ws, conn)

	time.Sleep(300 * time.Millisecond)
	if read.Load() == 0 {
		t.Fatal("the keepalive never sent anything, so a stop cannot be observed")
	}
	if err := ka.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	settled := read.Load()
	time.Sleep(400 * time.Millisecond)
	if read.Load() != settled {
		t.Errorf("bytes kept arriving after Close: %d then %d", settled, read.Load())
	}
}

// The wrapper must not swallow overlay traffic, and closing it must close the
// connection underneath.
func TestKeepalivePassesDataAndCloses(t *testing.T) {
	srv, _ := pingServer(t)
	ws, conn := dialTestWS(t, srv)
	ka := withKeepalive(ws, conn)

	if _, err := ka.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := ka.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := ka.Write([]byte("after close")); err == nil {
		t.Error("a write after Close succeeded, so the connection stayed open")
	}
}

// Closing twice must not panic on the stop channel.
func TestKeepaliveCloseIsIdempotent(t *testing.T) {
	srv, _ := pingServer(t)
	ws, conn := dialTestWS(t, srv)
	ka := withKeepalive(ws, conn)
	if err := ka.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := ka.Close(); err != nil {
		t.Logf("second close: %v", err)
	}
}

// pingsSeen turns bytes into a rough ping count. A WebSocket ping frame is a
// two byte header, a one byte length and a small payload, so anything near or
// above twenty bytes per ping is a conservative floor.
func pingsSeen(bytes int64) int {
	const bytesPerPing = 20
	return int(bytes / bytesPerPing)
}

// The keepalive has to be on the path the relay client actually uses, not only
// on the wrapper. A test of withKeepalive alone would pass while dialRelay
// stopped wrapping.
func TestDialRelayKeepsAnIdleLegAlive(t *testing.T) {
	srv, read := pingServer(t)

	oldInterval, oldWait := relayPingIntervalForTest, relayPingWaitForTest
	relayPingIntervalForTest = 50 * time.Millisecond
	relayPingWaitForTest = 50 * time.Millisecond
	defer func() {
		relayPingIntervalForTest, relayPingWaitForTest = oldInterval, oldWait
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := dialRelay(ctx, srv.URL)
	if err != nil {
		t.Fatalf("dialRelay: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// Read nothing. An idle overlay sends no traffic of its own, which is the
	// case the keepalive exists for.
	time.Sleep(150 * time.Millisecond)
	settled := read.Load()

	deadline := time.Now().Add(5 * time.Second)
	for read.Load() == settled && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if read.Load() == settled {
		t.Fatal("dialRelay sent no bytes while idle, so the leg would be dropped")
	}
}

package relay

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	// relayPingInterval is how often a relay leg sends a ping. Cloudflare
	// closes a proxied WebSocket after 100 seconds without traffic, and an
	// idle overlay moves no bytes at all, so without this the path drops
	// roughly every hundred seconds and has to be redialled.
	relayPingInterval = 30 * time.Second

	// relayPingWait bounds how long a ping waits for its pong. The pong is
	// only consumed while something is reading, and an idle overlay has no
	// reader, so the wait usually times out. That is fine: the ping frame is
	// already on the wire, which is what keeps the connection alive.
	relayPingWait = 10 * time.Second
)

// relayPingIntervalForTest and relayPingWaitForTest shorten the timings so the
// tests do not wait half a minute.
var (
	relayPingIntervalForTest = relayPingInterval
	relayPingWaitForTest     = relayPingWait
)

// keepaliveConn sends a ping on an interval so an idle relay leg stays open.
type keepaliveConn struct {
	net.Conn
	ws   *websocket.Conn
	stop chan struct{}
	once sync.Once
}

// withKeepalive starts the ping loop and returns conn. Closing conn stops it.
func withKeepalive(ws *websocket.Conn, conn net.Conn) net.Conn {
	k := &keepaliveConn{Conn: conn, ws: ws, stop: make(chan struct{})}
	go k.pingLoop(relayPingIntervalForTest, relayPingWaitForTest)
	return k
}

// pingLoop pings on an interval. Ping blocks until the peer answers or the wait
// runs out, so an idle leg, which has no reader to consume the pong, pings
// roughly every interval plus the wait. Both are well under the hundred
// seconds Cloudflare allows, which is what matters here.
func (k *keepaliveConn) pingLoop(interval, wait time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-k.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), wait)
			// A ping that waits out its pong still put a frame on the wire,
			// so a timeout here is the normal idle case and not a fault. The
			// next tick tries again either way.
			_ = k.ws.Ping(ctx)
			cancel()
		}
	}
}

func (k *keepaliveConn) Close() error {
	k.once.Do(func() { close(k.stop) })
	return k.Conn.Close()
}

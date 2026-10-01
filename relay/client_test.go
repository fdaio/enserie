package relay

import "testing"

func TestWebsocketHTTPClientHasNoTimeout(t *testing.T) {
	c := websocketHTTPClient()
	if c.Timeout != 0 {
		t.Fatalf("Timeout %s would kill a live splice", c.Timeout)
	}
}

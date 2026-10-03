package relay

import (
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// ListenAndServe starts a relay on addr. It returns the bound address and a
// function that closes the server.
func ListenAndServe(addr string) (net.Addr, func() error, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	hub := NewHub()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/", hub.Handler())
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr(), srv.Close, nil
}

// Handler returns the handler that upgrades a request to a WebSocket and
// serves it with the hub. A plain GET returns a text banner, so a wrong URL
// answers with readable text instead of a failed upgrade.
func (h *Hub) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.Header.Get("Upgrade") == "" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("enserie-relay websocket\n"))
			return
		}
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		conn := websocket.NetConn(r.Context(), ws, websocket.MessageBinary)
		h.Handle(pingConn{Conn: conn, ws: ws})
	})
}

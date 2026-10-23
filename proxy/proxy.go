// Package proxy carries TCP streams between two overlay nodes and exposes
// them through a local SOCKS5 listener. It needs no TUN and no privileges.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

const (
	// DefaultListen is the loopback address the SOCKS5 listener uses when the
	// caller gives no address.
	DefaultListen = "127.0.0.1:1080"
	// dialTimeout bounds one target connection on the peer side.
	dialTimeout = 15 * time.Second
	// handshakeTimeout bounds the SOCKS5 exchange and the target header.
	handshakeTimeout = 15 * time.Second
	// maxTarget is the largest target string a peer accepts.
	maxTarget = 1024
)

// Server is a SOCKS5 listener and the current peer session. Its methods are
// safe for concurrent use.
type Server struct {
	addr string
	logf func(string, ...any)

	// LocalIP is this node's overlay address. A target that names it is mapped
	// to loopback on this host, because proxy mode has no TUN to route it.
	// Set it before the first peer stream arrives.
	LocalIP net.IP

	mu     sync.Mutex
	ln     net.Listener
	sess   *yamux.Session
	conns  map[net.Conn]struct{}
	closed bool

	// ctx is canceled by Close. HandlePeer derives its context from this one,
	// so a shutdown unblocks the session and every stream without the caller
	// having to cancel its own context.
	ctx    context.Context
	cancel context.CancelFunc

	wg sync.WaitGroup
}

// New returns a server that will listen on addr. An empty addr uses
// DefaultListen, and a nil logf discards logs.
func New(addr string, logf func(string, ...any)) *Server {
	if addr == "" {
		addr = DefaultListen
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		addr:   addr,
		logf:   logf,
		conns:  make(map[net.Conn]struct{}),
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start binds the SOCKS5 listener. It does not wait for a peer. A request
// before a peer is connected fails with "network unreachable".
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return net.ErrClosed
	}
	if s.ln != nil {
		return fmt.Errorf("socks server already started")
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("socks listen %s: %w", s.addr, err)
	}
	s.ln = ln
	// Add under the lock. Close cannot pass the closed check until this
	// returns, so it never waits on a counter that is still zero.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.acceptLoop(ln)
	}()
	return nil
}

// Addr is the bound SOCKS5 address, or nil before Start.
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// Close stops the listener and the peer session, and closes every SOCKS5
// connection that is still open.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	ln := s.ln
	s.ln = nil
	sess := s.sess
	s.sess = nil
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	var err error
	if ln != nil {
		err = ln.Close()
	}
	if sess != nil {
		_ = sess.Close()
	}
	for _, c := range conns {
		_ = c.Close()
	}
	s.wg.Wait()
	return err
}

func (s *Server) acceptLoop(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if s.isClosed() {
				return
			}
			// A listener error that is not a close can repeat at once.
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if !s.track(c) {
			_ = c.Close()
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.untrack(c)
			defer c.Close()
			s.serveSOCKS(c)
		}()
	}
}

// HandlePeer multiplexes one authenticated peer connection. The invite side
// is the yamux server and the accepting side is the yamux client. Both sides
// can open streams, so either node can run a SOCKS5 listener.
func (s *Server) HandlePeer(ctx context.Context, c net.Conn, yamuxServer bool) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return net.ErrClosed
	}
	// Count the session before any stream handler, so Close waits for both.
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if s.ctx != nil {
		stop := context.AfterFunc(s.ctx, cancel)
		defer stop()
	}

	cfg := yamux.DefaultConfig()
	// The overlay connection is idle when no stream carries data. Keepalive
	// stops the QUIC and relay idle timers from closing a quiet session.
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 30 * time.Second
	cfg.StreamOpenTimeout = handshakeTimeout
	cfg.StreamCloseTimeout = 2 * time.Minute
	cfg.LogOutput = io.Discard

	var (
		sess *yamux.Session
		err  error
	)
	if yamuxServer {
		sess, err = yamux.Server(c, cfg)
	} else {
		sess, err = yamux.Client(c, cfg)
	}
	if err != nil {
		return err
	}
	s.setSession(sess)
	defer s.clearSession(sess)
	defer sess.Close()

	for {
		stream, err := sess.AcceptStreamWithContext(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, yamux.ErrSessionShutdown) {
				return nil
			}
			return err
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serveStream(ctx, stream)
		}()
	}
}

func (s *Server) setSession(sess *yamux.Session) {
	s.mu.Lock()
	old := s.sess
	s.sess = sess
	s.mu.Unlock()
	if old != nil && old != sess {
		_ = old.Close()
	}
}

func (s *Server) clearSession(sess *yamux.Session) {
	s.mu.Lock()
	if s.sess == sess {
		s.sess = nil
	}
	s.mu.Unlock()
}

func (s *Server) session() *yamux.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sess
}

// waitSession gives the peer path a moment to install its yamux session. The
// node reports a path just before the session starts, so a SOCKS request can
// arrive in that gap.
func (s *Server) waitSession(timeout time.Duration) *yamux.Session {
	if sess := s.session(); sess != nil {
		return sess
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.isClosed() {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
		if sess := s.session(); sess != nil {
			return sess
		}
	}
	return nil
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Server) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) untrack(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

// mapTarget turns this node's overlay address into loopback. A peer that asks
// for the overlay address wants a service on this host, not a packet on a TUN
// that proxy mode does not have.
func (s *Server) mapTarget(target string) string {
	if s.LocalIP == nil {
		return target
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return target
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.Equal(s.LocalIP) {
		return target
	}
	return net.JoinHostPort("127.0.0.1", port)
}

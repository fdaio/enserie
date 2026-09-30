package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/fdaio/enserie/relay"
)

var version = "dev"

func main() {
	listenDefault := envOr("ENSERIE_RELAY_LISTEN", "127.0.0.1:9090")
	addr := flag.String("listen", listenDefault, "TCP listen address (env ENSERIE_RELAY_LISTEN)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	lnAddr, closeFn, err := relay.ListenAndServe(*addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "enserie relay listening on http://%s (WebSocket)\n", lnAddr.String())
	fmt.Fprintln(os.Stderr, "blind splice only; put TLS at the edge for production")

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	_ = closeFn()
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

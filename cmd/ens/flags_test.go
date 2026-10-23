package main

import (
	"strings"
	"testing"

	"github.com/fdaio/enserie/overlay"
)

func TestParseInviteFlags(t *testing.T) {
	t.Run("defaults to proxy", func(t *testing.T) {
		opts, err := parseInviteFlags(nil)
		if err != nil {
			t.Fatalf("parseInviteFlags: %v", err)
		}
		if opts.mode != overlay.ModeProxy {
			t.Errorf("mode = %q, want %q", opts.mode, overlay.ModeProxy)
		}
		if opts.socksListen != defaultSocksListen {
			t.Errorf("socks listen = %q, want %q", opts.socksListen, defaultSocksListen)
		}
		if opts.subnet != "" {
			t.Errorf("subnet = %q, want empty so the default range stays", opts.subnet)
		}
	})

	t.Run("subnet is taken as given", func(t *testing.T) {
		opts, err := parseInviteFlags([]string{"--subnet", "10.99.0.0/24"})
		if err != nil {
			t.Fatalf("parseInviteFlags: %v", err)
		}
		if opts.subnet != "10.99.0.0/24" {
			t.Errorf("subnet = %q, want %q", opts.subnet, "10.99.0.0/24")
		}
	})

	t.Run("single dash also works", func(t *testing.T) {
		opts, err := parseInviteFlags([]string{"-subnet=10.99.0.0/24"})
		if err != nil {
			t.Fatalf("parseInviteFlags: %v", err)
		}
		if opts.subnet != "10.99.0.0/24" {
			t.Errorf("subnet = %q, want %q", opts.subnet, "10.99.0.0/24")
		}
	})

	t.Run("unknown flag is refused", func(t *testing.T) {
		_, err := parseInviteFlags([]string{"--relay", "http://127.0.0.1:9090"})
		if err == nil {
			t.Fatal("parseInviteFlags accepted --relay, which the CLI dropped")
		}
		if !strings.Contains(err.Error(), "usage: ens invite") {
			t.Errorf("error = %v, want it to show the usage", err)
		}
	})

	t.Run("positional argument is refused", func(t *testing.T) {
		_, err := parseInviteFlags([]string{"extra"})
		if err == nil {
			t.Fatal("parseInviteFlags accepted a positional argument")
		}
		if !strings.Contains(err.Error(), "no positional argument") {
			t.Errorf("error = %v, want it to mention the positional argument", err)
		}
	})

	t.Run("subnet without a value is refused", func(t *testing.T) {
		if _, err := parseInviteFlags([]string{"--subnet"}); err == nil {
			t.Fatal("parseInviteFlags accepted --subnet with no value")
		}
	})

	t.Run("subnet followed by an option is refused", func(t *testing.T) {
		// The flag package would read "--relay" as the subnet value, so the
		// error has to name the option instead of a broken CIDR.
		_, err := parseInviteFlags([]string{"--subnet", "--relay", "http://127.0.0.1:9090"})
		if err == nil {
			t.Fatal("parseInviteFlags accepted --subnet followed by an option")
		}
		if !strings.Contains(err.Error(), "--subnet needs a network") {
			t.Errorf("error = %v, want it to ask for a network", err)
		}
	})

	t.Run("tun selects the TUN mode", func(t *testing.T) {
		opts, err := parseInviteFlags([]string{"--tun"})
		if err != nil {
			t.Fatalf("parseInviteFlags: %v", err)
		}
		if opts.mode != overlay.ModeTun {
			t.Errorf("mode = %q, want %q", opts.mode, overlay.ModeTun)
		}
	})

	t.Run("tun and proxy are exclusive", func(t *testing.T) {
		if _, err := parseInviteFlags([]string{"--tun", "--proxy"}); err == nil {
			t.Fatal("parseInviteFlags accepted --tun and --proxy together")
		}
	})

	t.Run("tun refuses a socks address", func(t *testing.T) {
		_, err := parseInviteFlags([]string{"--tun", "--socks-listen", "127.0.0.1:1081"})
		if err == nil {
			t.Fatal("parseInviteFlags accepted a socks address in TUN mode")
		}
		if !strings.Contains(err.Error(), "drop --tun") {
			t.Errorf("error = %v, want it to name the conflict", err)
		}
	})

	t.Run("socks address needs a port", func(t *testing.T) {
		_, err := parseInviteFlags([]string{"--socks-listen", "127.0.0.1"})
		if err == nil {
			t.Fatal("parseInviteFlags accepted a socks address without a port")
		}
		if !strings.Contains(err.Error(), "HOST:PORT") {
			t.Errorf("error = %v, want it to ask for HOST:PORT", err)
		}
	})
}

func TestParseInviteFlagsAdvertise(t *testing.T) {
	t.Run("advertise is taken as given", func(t *testing.T) {
		opts, err := parseInviteFlags([]string{"--advertise", "203.0.113.7"})
		if err != nil {
			t.Fatalf("parseInviteFlags: %v", err)
		}
		if opts.advertise != "203.0.113.7" {
			t.Errorf("advertise = %q, want %q", opts.advertise, "203.0.113.7")
		}
	})

	t.Run("a hostname is accepted", func(t *testing.T) {
		opts, err := parseInviteFlags([]string{"--advertise", "home.example.com"})
		if err != nil {
			t.Fatalf("parseInviteFlags: %v", err)
		}
		if opts.advertise != "home.example.com" {
			t.Errorf("advertise = %q, want the hostname", opts.advertise)
		}
	})

	t.Run("a port is dropped", func(t *testing.T) {
		// The listener picks its own port, so advertising one would point
		// the peer at nothing.
		opts, err := parseInviteFlags([]string{"--advertise", "203.0.113.7:9999"})
		if err != nil {
			t.Fatalf("parseInviteFlags: %v", err)
		}
		if opts.advertise != "203.0.113.7" {
			t.Errorf("advertise = %q, want the port dropped", opts.advertise)
		}
	})

	t.Run("advertise followed by an option is refused", func(t *testing.T) {
		_, err := parseInviteFlags([]string{"--advertise", "--subnet", "10.99.0.0/24"})
		if err == nil {
			t.Fatal("parseInviteFlags accepted --advertise followed by an option")
		}
		if !strings.Contains(err.Error(), "--advertise needs an address") {
			t.Errorf("error = %v, want it to ask for an address", err)
		}
	})

	t.Run("subnet and advertise combine", func(t *testing.T) {
		opts, err := parseInviteFlags([]string{"--subnet", "10.99.0.0/24", "--advertise", "203.0.113.7"})
		if err != nil {
			t.Fatalf("parseInviteFlags: %v", err)
		}
		if opts.subnet != "10.99.0.0/24" || opts.advertise != "203.0.113.7" {
			t.Errorf("got subnet=%q advertise=%q", opts.subnet, opts.advertise)
		}
	})
}

func TestParseAcceptFlags(t *testing.T) {
	t.Run("token and default socks address", func(t *testing.T) {
		opts, token, err := parseAcceptFlags([]string{"TOKEN"})
		if err != nil {
			t.Fatalf("parseAcceptFlags: %v", err)
		}
		if token != "TOKEN" {
			t.Errorf("token = %q, want TOKEN", token)
		}
		if opts.socksListen != defaultSocksListen {
			t.Errorf("socks listen = %q, want %q", opts.socksListen, defaultSocksListen)
		}
	})

	t.Run("socks address is taken as given", func(t *testing.T) {
		opts, _, err := parseAcceptFlags([]string{"--socks-listen", "127.0.0.1:1081", "TOKEN"})
		if err != nil {
			t.Fatalf("parseAcceptFlags: %v", err)
		}
		if opts.socksListen != "127.0.0.1:1081" {
			t.Errorf("socks listen = %q, want 127.0.0.1:1081", opts.socksListen)
		}
	})

	t.Run("token is required", func(t *testing.T) {
		if _, _, err := parseAcceptFlags(nil); err == nil {
			t.Fatal("parseAcceptFlags accepted no token")
		}
	})

	t.Run("two tokens are refused", func(t *testing.T) {
		if _, _, err := parseAcceptFlags([]string{"ONE", "TWO"}); err == nil {
			t.Fatal("parseAcceptFlags accepted two tokens")
		}
	})

	t.Run("socks address needs a port", func(t *testing.T) {
		if _, _, err := parseAcceptFlags([]string{"--socks-listen", "127.0.0.1", "TOKEN"}); err == nil {
			t.Fatal("parseAcceptFlags accepted a socks address without a port")
		}
	})
}

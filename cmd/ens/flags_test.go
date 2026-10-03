package main

import (
	"strings"
	"testing"
)

func TestParseInviteFlags(t *testing.T) {
	t.Run("no subnet keeps the default range", func(t *testing.T) {
		subnet, err := parseInviteFlags(nil)
		if err != nil {
			t.Fatalf("parseInviteFlags: %v", err)
		}
		if subnet != "" {
			t.Errorf("subnet = %q, want empty so the default range stays", subnet)
		}
	})

	t.Run("subnet is taken as given", func(t *testing.T) {
		subnet, err := parseInviteFlags([]string{"--subnet", "10.99.0.0/24"})
		if err != nil {
			t.Fatalf("parseInviteFlags: %v", err)
		}
		if subnet != "10.99.0.0/24" {
			t.Errorf("subnet = %q, want %q", subnet, "10.99.0.0/24")
		}
	})

	t.Run("single dash also works", func(t *testing.T) {
		subnet, err := parseInviteFlags([]string{"-subnet=10.99.0.0/24"})
		if err != nil {
			t.Fatalf("parseInviteFlags: %v", err)
		}
		if subnet != "10.99.0.0/24" {
			t.Errorf("subnet = %q, want %q", subnet, "10.99.0.0/24")
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
}

package main

import (
	"strings"
	"testing"
)

func TestDefaultRelays(t *testing.T) {
	got := splitCSV(defaultRelays)
	if len(got) != 2 {
		t.Fatalf("got %d relays, want 2 from %q", len(got), defaultRelays)
	}
	for _, u := range got {
		if !strings.HasPrefix(u, "https://relay-") || !strings.HasSuffix(u, ".getfda.dev") {
			t.Errorf("unexpected hosted relay %q", u)
		}
	}
}

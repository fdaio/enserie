//go:build linux

package overlay

import (
	"fmt"
	"testing"

	"golang.org/x/sys/unix"
)

func TestIsTUNBusy(t *testing.T) {
	if !isTUNBusy(fmt.Errorf("TUNSETIFF enserie0: %w", unix.EBUSY)) {
		t.Fatal("EBUSY should be busy")
	}
	if !isTUNBusy(unix.EEXIST) {
		t.Fatal("EEXIST should be busy")
	}
	if isTUNBusy(unix.EPERM) {
		t.Fatal("EPERM is not busy")
	}
}

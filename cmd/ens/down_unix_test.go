package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A root instance locks <runtime dir>/ens.lock while an unprivileged one locks
// a file in the temporary directory. An unprivileged `ens down` used to look
// only at its own path and report "ens is not running" while the overlay was
// up, which is the report that started this.
func TestOtherLockPathIsUsable(t *testing.T) {
	other := otherLockPath()
	if other == "" {
		t.Skip("testLockPath is set")
	}
	if other == lockPath() {
		t.Fatalf("otherLockPath = %s, which is this user's own lock path", other)
	}
	if _, err := os.Stat(filepath.Dir(other)); err != nil {
		t.Errorf("other lock path %s: directory missing: %v", other, err)
	}
}

func TestReadWorkerPIDAt(t *testing.T) {
	dir := t.TempDir()
	if _, err := readWorkerPIDAt(filepath.Join(dir, "absent.lock")); !errors.Is(err, errNotRunning) {
		t.Errorf("missing lock: err = %v, want errNotRunning", err)
	}

	garbage := filepath.Join(dir, "garbage.lock")
	if err := os.WriteFile(garbage, []byte("not a pid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readWorkerPIDAt(garbage); !errors.Is(err, errNotRunning) {
		t.Errorf("garbage lock: err = %v, want errNotRunning", err)
	}

	// pid 1 is init. A lock naming it did not come from an ens worker.
	one := filepath.Join(dir, "one.lock")
	if err := os.WriteFile(one, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readWorkerPIDAt(one); !errors.Is(err, errNotRunning) {
		t.Errorf("pid 1 lock: err = %v, want errNotRunning", err)
	}

	self := filepath.Join(dir, "self.lock")
	if err := os.WriteFile(self, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	pid, err := readWorkerPIDAt(self)
	if err != nil {
		t.Fatalf("own pid lock: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("pid = %d, want %d", pid, os.Getpid())
	}
}

func TestIsLive(t *testing.T) {
	if !isLive(os.Getpid()) {
		t.Error("this process should be live")
	}
	if isLive(unusedPID()) {
		t.Errorf("pid %d reported live", unusedPID())
	}
	// waitGone reports a pid that never goes away by running out the clock,
	// which is what stops a stop from blocking forever.
	if waitGone(os.Getpid(), 200*time.Millisecond) {
		t.Error("waitGone reported this process as gone while it is live")
	}
}

// waitGone has to return the moment the pid leaves rather than always burning
// the timeout, or every stop costs the full grace period.
func TestWaitGoneOnMissingProcess(t *testing.T) {
	start := time.Now()
	if !waitGone(unusedPID(), 30*time.Second) {
		t.Fatal("waitGone should report a missing process as gone")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waitGone took %s for a missing process, want an immediate return", elapsed)
	}
}

// The message has to name the remedy. "not running" beside a live overlay is
// what sent the original report in circles.
func TestDownErrorNamesTheRunningRootInstance(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "ens.lock")
	if err := os.WriteFile(other, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}

	err := downError(errNotRunning, other)
	if !strings.Contains(err.Error(), "sudo ens down") {
		t.Errorf("error = %v, want it to name sudo ens down", err)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(os.Getpid())) {
		t.Errorf("error = %v, want it to name the pid", err)
	}
}

func TestDownErrorStaysQuietWithoutAnotherInstance(t *testing.T) {
	dir := t.TempDir()
	if err := downError(errNotRunning, filepath.Join(dir, "absent.lock")); !errors.Is(err, errNotRunning) {
		t.Errorf("error = %v, want errNotRunning", err)
	}
	// An empty other path is the case where this user's lock is the only one.
	if err := downError(errNotRunning, ""); !errors.Is(err, errNotRunning) {
		t.Errorf("error = %v, want errNotRunning", err)
	}
}

// A real I/O failure must not be rewritten into "not running".
func TestDownErrorKeepsRealFailures(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "sub")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	real := errors.New("permission denied")
	if err := downError(real, blocked); !errors.Is(err, real) {
		t.Errorf("error = %v, want the original failure", err)
	}
}

// unusedPID returns a pid that is not in use. It starts above the usual pid_max
// so a collision is unlikely, and the caller retries.
func unusedPID() int {
	const high = 4194303
	for range 20 {
		if !isLive(high) {
			return high
		}
		time.Sleep(20 * time.Millisecond)
	}
	return high
}

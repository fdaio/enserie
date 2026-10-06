package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A worker writes its error to the status pipe and then exits non-zero, so the
// scanner and the wait channel are ready at the same moment. Select picks
// between ready cases at random, so the real reason was replaced by the exit
// status about half the time. That is how "ens already running" turned into
// "ens worker exited: exit status 1" on the way to the same failure.
func TestWaitConnectedPrefersTheReportedError(t *testing.T) {
	const runs = 40
	var masked int
	for range runs {
		statusR, statusW, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		waitCh := make(chan error, 1)
		go func() {
			// The worker writes first, then exits. Both become ready together.
			time.Sleep(5 * time.Millisecond)
			_, _ = statusW.WriteString("error: ens already running; stop it first with: sudo ens down\n")
			_ = statusW.Close()
			waitCh <- errors.New("exit status 1")
		}()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err = waitConnected(ctx, statusR, waitCh, func() error { return nil })
		_ = statusR.Close()
		if err == nil {
			t.Fatal("waitConnected returned nil")
		}
		if !strings.Contains(err.Error(), "ens already running") {
			masked++
		}
	}
	if masked > 0 {
		t.Errorf("%d of %d runs reported the exit status instead of the reason", masked, runs)
	}
}

// When the worker dies without saying anything, the exit status is all there
// is and it has to be reported rather than lost.
func TestWaitConnectedReportsAnExitStatusWhenThereIsNoMessage(t *testing.T) {
	statusR, statusW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	waitCh := make(chan error, 1)
	go func() {
		time.Sleep(5 * time.Millisecond)
		_ = statusW.Close()
		waitCh <- errors.New("exit status 1")
	}()
	defer func() { _ = statusR.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = waitConnected(ctx, statusR, waitCh, func() error { return nil })
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("error = %v, want the exit status", err)
	}
}

// A connected worker must still succeed; the pause must not turn a good path
// into a failure.
func TestWaitConnectedStillReportsConnected(t *testing.T) {
	statusR, statusW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	waitCh := make(chan error, 1)
	go func() {
		time.Sleep(5 * time.Millisecond)
		_, _ = statusW.WriteString("connected\n")
		_ = statusW.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	err = waitConnected(ctx, statusR, waitCh, func() error { return nil })
	_ = statusR.Close()
	if err != nil {
		t.Fatalf("waitConnected: %v", err)
	}
	if elapsed := time.Since(start); elapsed > statusGrace {
		t.Errorf("waitConnected took %s on the connected path, want no extra pause", elapsed)
	}
}

// ens down must not report a stop that did not happen. A worker that ignores
// SIGTERM keeps the lock, so saying "stopped" leaves the next ens invite
// failing with "ens already running".
func TestRunDownDoesNotClaimSuccessWhenTheProcessSurvives(t *testing.T) {
	dir := t.TempDir()
	testLockPath = filepath.Join(dir, "ens.lock")
	t.Cleanup(func() { testLockPath = "" })

	// A shell that traps and ignores SIGTERM stands in for a worker wedged in a
	// shutdown that never finishes.
	cmd := exec.Command("sh", "-c", "trap '' TERM; while :; do sleep 1; done")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	f, err := os.Create(testLockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeLockPID(f, cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	// Shorten the grace so the test does not wait ten seconds.
	old := stopGraceForTest
	stopGraceForTest = 300 * time.Millisecond
	defer func() { stopGraceForTest = old }()

	err = runDown()
	if err == nil {
		t.Fatal("runDown claimed a stop that did not happen")
	}
	if !strings.Contains(err.Error(), "kill -9") {
		t.Errorf("error = %v, want it to say how to force the stop", err)
	}
	if !strings.Contains(err.Error(), "still running") {
		t.Errorf("error = %v, want it to say the process is still running", err)
	}
}

// A process that does exit on SIGTERM must still be reported as stopped, so the
// fix does not turn a working stop into a failure.
func TestRunDownReportsAStopThatWorked(t *testing.T) {
	dir := t.TempDir()
	testLockPath = filepath.Join(dir, "ens.lock")
	t.Cleanup(func() { testLockPath = "" })

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(reaped)
	}()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	f, err := os.Create(testLockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeLockPID(f, cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	if err := runDown(); err != nil {
		t.Fatalf("runDown: %v", err)
	}
	select {
	case <-reaped:
	case <-time.After(2 * time.Second):
		t.Fatal("the process did not exit on SIGTERM")
	}
}

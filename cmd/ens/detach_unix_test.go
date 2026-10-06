//go:build unix

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
	"time"
)

func TestMaybeSuperviseWorkerRunsFn(t *testing.T) {
	t.Setenv(workerEnv, "1")
	called := false
	if err := maybeSupervise(func() error {
		called = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("run not called")
	}
}

// captureStderr collects what fn writes to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	fn()
	os.Stderr = saved
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	return string(out)
}

func TestReportFailurePrintsOnSupervisorOnly(t *testing.T) {
	failure := fmtErr("tun/listen: operation not permitted (need root)")

	t.Setenv(workerEnv, "")
	if got := captureStderr(t, func() { reportFailure(failure) }); got != failure.Error()+"\n" {
		t.Errorf("supervisor printed %q, want %q", got, failure.Error()+"\n")
	}

	t.Setenv(workerEnv, "1")
	if got := captureStderr(t, func() { reportFailure(failure) }); got != "" {
		t.Errorf("worker printed %q, want nothing", got)
	}
}

func TestWaitConnectedSeesStatus(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	waitCh := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- waitConnected(ctx, r, waitCh, func() error { return nil })
	}()
	if _, err := io.WriteString(w, statusConnected+"\n"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWaitConnectedKillsOnCancel(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	killed := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	waitCh := make(chan error)
	done := make(chan error, 1)
	go func() {
		done <- waitConnected(ctx, r, waitCh, func() error {
			killed <- struct{}{}
			return nil
		})
	}()
	cancel()
	if err := <-done; err == nil {
		t.Fatal("expected cancel error")
	}
	select {
	case <-killed:
	case <-time.After(time.Second):
		t.Fatal("kill not called")
	}
}

func TestWaitConnectedWorkerDeath(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	waitCh := make(chan error, 1)
	waitCh <- fmtErr("exit 1")
	err = waitConnected(context.Background(), r, waitCh, func() error { return nil })
	if err == nil || !strings.Contains(err.Error(), "worker exited") {
		t.Fatalf("got %v", err)
	}
}

type fmtErr string

func (e fmtErr) Error() string { return string(e) }

func TestWaitConnectedSurfacesWorkerErrorLine(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	waitCh := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		done <- waitConnected(context.Background(), r, waitCh, func() error { return nil })
	}()
	if _, err := io.WriteString(w, statusErrorPref+"ens already running\n"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	err = <-done
	if err == nil || !strings.Contains(err.Error(), "ens already running") {
		t.Fatalf("got %v", err)
	}
}

func TestWaitConnectedEOFPrefersExitError(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	waitCh := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		done <- waitConnected(context.Background(), r, waitCh, func() error { return nil })
	}()
	_ = w.Close()
	go func() {
		time.Sleep(50 * time.Millisecond)
		waitCh <- fmtErr("exit status 1")
	}()
	err = <-done
	if err == nil || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("got %v", err)
	}
}

func TestReadWorkerPIDMissing(t *testing.T) {
	testLockPath = filepath.Join(t.TempDir(), "missing.lock")
	t.Cleanup(func() { testLockPath = "" })
	if _, err := readWorkerPID(); err == nil {
		t.Fatal("expected not running")
	}
}

func TestWriteAndReadLockPID(t *testing.T) {
	dir := t.TempDir()
	testLockPath = filepath.Join(dir, "ens.lock")
	t.Cleanup(func() { testLockPath = "" })
	f, err := os.Create(testLockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeLockPID(f, 4242); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	pid, err := readWorkerPID()
	if err != nil || pid != 4242 {
		t.Fatalf("pid %d %v", pid, err)
	}
}

func TestRunDownMissing(t *testing.T) {
	testLockPath = filepath.Join(t.TempDir(), "ens.lock")
	t.Cleanup(func() { testLockPath = "" })
	if err := runDown(); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("got %v", err)
	}
}

func TestRunDownSignalsChild(t *testing.T) {
	dir := t.TempDir()
	testLockPath = filepath.Join(dir, "ens.lock")
	t.Cleanup(func() { testLockPath = "" })
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	// Reap as soon as the child dies. A zombie still answers signal 0, and a
	// worker that ens down stops is not a child of the process running down,
	// so it never becomes this process's zombie.
	reaped := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(reaped)
	}()
	f, err := os.Create(testLockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeLockPID(f, cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := runDown(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reaped:
	case <-time.After(2 * time.Second):
		t.Fatal("child still running")
	}
}

// A failed redirect must say so. Without the redirect the worker keeps writing
// to the terminal, so the shell looks like it never returned and the logs give
// no hint why.
func TestRedirectLogsReportsAFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir", "ens.log")
	out := captureStderr(t, func() {
		if err := redirectLogsTo(missing); err == nil {
			t.Error("redirectLogsTo succeeded with a path it cannot create")
		}
	})
	if !strings.Contains(out, "ens.log") {
		t.Errorf("output = %q, want it to name the log path", out)
	}
	if !strings.Contains(out, "terminal") {
		t.Errorf("output = %q, want it to say the logs stay on the terminal", out)
	}
}

func TestRedirectLogsSucceedsQuietly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ens.log")
	// redirectLogsTo repoints fd 1 and 2, so restore them afterwards.
	savedOut, err := unix.Dup(1)
	if err != nil {
		t.Fatal(err)
	}
	savedErr, err := unix.Dup(2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = unix.Dup2(savedOut, 1)
		_ = unix.Dup2(savedErr, 2)
		_ = unix.Close(savedOut)
		_ = unix.Close(savedErr)
	})

	if err := redirectLogsTo(path); err != nil {
		t.Fatalf("redirectLogsTo: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("log file was not created: %v", err)
	}
}

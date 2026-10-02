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
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
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
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("child exited 0 after SIGTERM")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("child still running")
	}
}

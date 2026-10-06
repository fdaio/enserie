//go:build unix

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// stopGrace is how long ens down waits for the worker to exit. Reporting a stop
// before the worker is gone leaves the lock held, and the next `ens invite`
// then fails with "ens already running".
const stopGrace = 10 * time.Second

func runDown() error {
	pid, err := readWorkerPIDAt(lockPath())
	if err == nil && isLive(pid) {
		proc, findErr := os.FindProcess(pid)
		if findErr != nil {
			return downError(errNotRunning, otherLockPath())
		}
		if sigErr := proc.Signal(syscall.SIGTERM); sigErr != nil {
			if errors.Is(sigErr, syscall.EPERM) {
				return fmt.Errorf("ens is running as another user (pid %d); stop it with: sudo ens down", pid)
			}
			return sigErr
		}
		waitGone(pid, stopGrace)
		fmt.Fprintf(os.Stderr, "ens: stopped pid %d\n", pid)
		return nil
	}
	return downError(errNotRunning, otherLockPath())
}

// downError explains why this user's lock file did not name a running worker.
// An instance started at another privilege level keeps its lock elsewhere, and
// reporting "ens is not running" while the overlay is up sends the reader in
// circles, so name the instance and the command that stops it.
func downError(err error, other string) error {
	if !errors.Is(err, errNotRunning) {
		return err
	}
	if other != "" {
		if pid, otherErr := readWorkerPIDAt(other); otherErr == nil && isLive(pid) {
			return fmt.Errorf("ens is running as root (pid %d); stop it with: sudo ens down", pid)
		}
	}
	return err
}

// isLive reports whether a process exists.
//
// Signal 0 answers with EPERM when the process belongs to another user, which
// is the normal case for a root worker seen from an unprivileged shell. Only
// ESRCH means the process is gone. Treating EPERM as "not running" is what made
// ens down report that nothing was up while a root overlay was running.
//
// Signal 0 also succeeds for a zombie, so a child that has exited but not been
// reaped reads as live. ens only signals workers it did not start, and those
// are reparented, so that does not arise here.
func isLive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	sigErr := proc.Signal(syscall.Signal(0))
	if sigErr == nil {
		return true
	}
	return errors.Is(sigErr, syscall.EPERM)
}

// waitGone polls until the pid is gone or the timeout passes. The caller
// reports the timeout rather than blocking forever, because a stop that never
// completes is worse than one that says so.
func waitGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !isLive(pid) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

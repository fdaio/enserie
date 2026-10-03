//go:build unix

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// testLockPath overrides the lock file in tests.
var testLockPath string

func lockPath() string {
	if testLockPath != "" {
		return testLockPath
	}
	if os.Geteuid() != 0 {
		return filepath.Join(os.TempDir(), "ens.lock")
	}
	return "/run/ens.lock"
}

func logPath() string {
	if os.Geteuid() != 0 {
		return filepath.Join(os.TempDir(), "ens.log")
	}
	return "/run/ens.log"
}

func acquireInstanceLock() (*os.File, error) {
	f, err := os.OpenFile(lockPath(), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("ens already running; stop it first with: sudo ens down")
	}
	if err := writeLockPID(f, os.Getpid()); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func writeLockPID(f *os.File, pid int) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	_, err := fmt.Fprintf(f, "%d\n", pid)
	return err
}

func readWorkerPID() (int, error) {
	b, err := os.ReadFile(lockPath())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("ens is not running")
		}
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return 0, fmt.Errorf("ens is not running")
	}
	return pid, nil
}

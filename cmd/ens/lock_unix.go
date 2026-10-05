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

// runtimeDir is where the lock and the log live when running as root.
//
// Linux keeps /run, which is a tmpfs that the system clears on boot. macOS has
// no /run at all, so a hardcoded path there fails with "no such file or
// directory" before any TUN work starts. /var/run is the same directory on
// Linux, through a symlink, and the closest equivalent macOS has.
//
// Both are root-owned, which matters: a lock in a world-writable directory
// would let another user pre-create the file.
var runtimeDir = func() string {
	for _, dir := range []string{"/run", "/var/run"} {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir
		}
	}
	return filepath.Join(os.TempDir(), "ens")
}

func lockPath() string {
	return lockPathFor(os.Geteuid())
}

// lockPathFor takes the effective user id so that a test can check the root
// branch on a runner that is not root. Otherwise the check that matters most
// only ever runs on one developer's machine.
func lockPathFor(euid int) string {
	if testLockPath != "" {
		return testLockPath
	}
	if euid != 0 {
		return filepath.Join(os.TempDir(), "ens.lock")
	}
	return filepath.Join(runtimeDir(), "ens.lock")
}

func logPath() string {
	return logPathFor(os.Geteuid())
}

func logPathFor(euid int) string {
	if euid != 0 {
		return filepath.Join(os.TempDir(), "ens.log")
	}
	return filepath.Join(runtimeDir(), "ens.log")
}

func acquireInstanceLock() (*os.File, error) {
	return acquireInstanceLockAt(lockPath())
}

func acquireInstanceLockAt(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
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

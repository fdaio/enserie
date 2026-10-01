//go:build unix

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func acquireInstanceLock() (*os.File, error) {
	path := "/run/ens.lock"
	if os.Geteuid() != 0 {
		path = filepath.Join(os.TempDir(), "ens.lock")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("ens already running")
	}
	return f, nil
}

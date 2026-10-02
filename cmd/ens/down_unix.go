//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
)

func runDown() error {
	pid, err := readWorkerPID()
	if err != nil {
		return err
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("ens is not running")
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return fmt.Errorf("ens is not running")
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "ens: stopped pid %d\n", pid)
	return nil
}

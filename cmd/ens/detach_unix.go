//go:build unix

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	workerEnv       = "ENSERIE_WORKER"
	statusConnected = "connected"
	statusErrorPref = "error: "
	statusFD        = 3
	// statusGrace is how long a supervisor waits for a message the worker
	// already wrote before falling back to the exit status.
	statusGrace = 500 * time.Millisecond
)

func isWorker() bool {
	return os.Getenv(workerEnv) == "1"
}

func maybeSupervise(run func() error) error {
	if isWorker() {
		if err := run(); err != nil {
			reportError(err)
			return err
		}
		return nil
	}
	return superviseWorker()
}

func superviseWorker() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	statusR, statusW, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd := workerCommand(exe, os.Args[1:], statusW)
	if err := cmd.Start(); err != nil {
		_ = statusR.Close()
		_ = statusW.Close()
		return err
	}
	_ = statusW.Close()
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	err = waitConnected(ctx, statusR, waitCh, func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	})
	_ = statusR.Close()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "ens: running in background (pid %d); ens down to stop\n", cmd.Process.Pid)
	return nil
}

func workerCommand(exe string, args []string, statusW *os.File) *exec.Cmd {
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), workerEnv+"=1")
	cmd.ExtraFiles = []*os.File{statusW}
	cmd.Stdin = nil
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

// waitConnected returns when the worker writes "connected".
// Ctrl-C kills the worker. A worker exit before that is an error.
func waitConnected(ctx context.Context, r io.Reader, waitCh <-chan error, kill func() error) error {
	lines := make(chan struct{}, 1)
	readErr := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			line := sc.Text()
			if line == statusConnected {
				lines <- struct{}{}
				return
			}
			if msg, ok := strings.CutPrefix(line, statusErrorPref); ok {
				readErr <- fmt.Errorf("ens worker failed: %s", msg)
				return
			}
		}
		if err := sc.Err(); err != nil {
			readErr <- err
			return
		}
		readErr <- io.EOF
	}()
	for {
		select {
		case <-ctx.Done():
			_ = kill()
			return ctx.Err()
		case err := <-waitCh:
			if err != nil {
				// A worker writes its error to the status pipe before it
				// exits, so both this case and the scanner are ready at once
				// and the select picks between them at random. Without this
				// pause the real reason is replaced by the exit status half
				// the time, which is how "ens already running" turned into
				// "ens worker exited: exit status 1".
				select {
				case rerr := <-readErr:
					if rerr != nil && !errors.Is(rerr, io.EOF) {
						return rerr
					}
				case <-time.After(statusGrace):
				}
				return fmt.Errorf("ens worker exited: %w", err)
			}
			return fmt.Errorf("ens worker exited before connect")
		case <-lines:
			return nil
		case err := <-readErr:
			if err != io.EOF {
				return err
			}
			// The worker closed the status pipe without saying why.
			// Give its exit status a moment to arrive so we can
			// report the real cause instead of a bare EOF.
			select {
			case werr := <-waitCh:
				if werr != nil {
					return fmt.Errorf("ens worker exited: %w", werr)
				}
				return fmt.Errorf("ens worker exited before connect")
			case <-time.After(3 * time.Second):
				return fmt.Errorf("ens worker status pipe closed before connected")
			}
		}
	}
}

func notifyConnected() {
	f := os.NewFile(uintptr(statusFD), "ens-status")
	if f == nil {
		return
	}
	_, _ = fmt.Fprintln(f, statusConnected)
	_ = f.Close()
}

// reportError tells the supervisor why the worker failed before connecting.
func reportError(err error) {
	f := os.NewFile(uintptr(statusFD), "ens-status")
	if f == nil {
		return
	}
	_, _ = fmt.Fprintln(f, statusErrorPref+err.Error())
	_ = f.Close()
}

// redirectLogs sends the worker's output to the log file. A failure is
// reported on the terminal rather than swallowed: without the redirect the
// worker keeps writing to the terminal, so the shell looks like it never
// returned, and the logs give no hint why.
func redirectLogs() {
	redirectLogsTo(logPath())
}

func redirectLogsTo(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ens: cannot write the log to %s: %v\n", path, err)
		fmt.Fprintf(os.Stderr, "ens: logs stay on this terminal until you stop the worker\n")
		return err
	}
	_ = unix.Dup2(int(f.Fd()), 1)
	_ = unix.Dup2(int(f.Fd()), 2)
	_ = f.Close()
	return nil
}

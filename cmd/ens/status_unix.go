//go:build unix

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// logTailLines is how much of the log status shows. The log grows for as long
// as the overlay is up, and a status that printed all of it would be no easier
// to read than the log itself.
const logTailLines = 8

// statusLock, statusOtherLock and statusLog override the paths runStatus reads.
// They are empty outside a test, which leaves the real runtime paths in place.
var statusLock, statusOtherLock, statusLog string

// useStatusPaths points status at one instance and returns a function that
// restores the previous paths.
func useStatusPaths(lock, log string) func() {
	oldLock, oldOther, oldLog := statusLock, statusOtherLock, statusLog
	statusLock, statusOtherLock, statusLog = lock, "", log
	return func() { statusLock, statusOtherLock, statusLog = oldLock, oldOther, oldLog }
}

// runStatus reports what is already known about the running worker. It reads
// the lock and the log rather than asking the worker, so it works while the
// worker runs as another user.
//
// The connection path and the overlay addresses stay in the worker's memory,
// so they are not in here. They would have to be written to a file that could
// then go stale.
func runStatus(out io.Writer) error {
	own, other := lockPath(), otherLockPath()
	log := ""
	if statusLock != "" {
		own, other, log = statusLock, statusOtherLock, statusLog
	}
	inst, err := findRunningInstanceAt(own, other)
	if err != nil {
		return err
	}
	if log != "" {
		inst.log = log
	}
	return printStatus(out, inst)
}

// instance is a running worker and where its state lives.
type instance struct {
	pid   int
	level string
	log   string
}

// findRunningInstanceAt returns the live worker, whichever privilege level it
// runs at.
//
// Both lock files are checked because a root worker and an unprivileged one
// keep separate locks. The reader's own lock is checked first, so a reader who
// is also the operator reports their own instance.
func findRunningInstanceAt(ownLock, otherLock string) (instance, error) {
	for _, candidate := range []struct {
		lock  string
		level string
		euid  int
	}{
		{ownLock, "this user", os.Geteuid()},
		// A lock in the root runtime directory belongs to a root worker, which
		// an unprivileged reader still has to be able to see. Both the lock
		// and the log are world readable for that reason.
		{otherLock, "root", 0},
	} {
		if candidate.lock == "" {
			continue
		}
		pid, err := readWorkerPIDAt(candidate.lock)
		if err != nil || !isLive(pid) {
			continue
		}
		return instance{
			pid:   pid,
			level: candidate.level,
			log:   logPathFor(candidate.euid),
		}, nil
	}
	return instance{}, errNotRunning
}

// printStatus writes what is known about a running instance.
func printStatus(out io.Writer, inst instance) error {
	fmt.Fprintf(out, "  state:   running (pid %d, as %s)\n", inst.pid, inst.level)
	fmt.Fprintf(out, "  log:     %s\n", inst.log)

	if tail, err := tailFile(inst.log, logTailLines); err == nil && len(tail) > 0 {
		fmt.Fprintln(out)
		for _, line := range tail {
			fmt.Fprintf(out, "  %s\n", line)
		}
	}
	// A reader who is also the operator can stop the instance with the binary
	// they already have. One reading somebody else's instance cannot, because
	// the path names this process rather than the one running.
	stop := fmt.Sprintf("%s stops it.", sudoCommand("down"))
	if inst.level != "this user" {
		stop = fmt.Sprintf("%s stops it.", sudoCommand("down"))
	}
	fmt.Fprintf(out, "\n%s\n", stop)
	return nil
}

// tailFile returns the last n lines of a file.
func tailFile(path string, n int) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

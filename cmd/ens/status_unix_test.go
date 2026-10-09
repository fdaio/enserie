//go:build unix

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The pid in a lock file is only meaningful while the process is alive, so a
// stale file has to read as not running rather than as a dead pid.
func TestFindRunningInstanceIgnoresAStaleLock(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "ens.lock")
	// A pid this high is not in use, so isLive answers false for it.
	if err := os.WriteFile(lock, []byte("4194304\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := findRunningInstanceAt(lock, ""); !errors.Is(err, errNotRunning) {
		t.Fatalf("err = %v, want errNotRunning", err)
	}
}

func TestFindRunningInstanceReportsTheLiveProcess(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "ens.lock")
	if err := os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := findRunningInstanceAt(lock, "")
	if err != nil {
		t.Fatalf("findRunningInstanceAt: %v", err)
	}
	if got.pid != os.Getpid() {
		t.Fatalf("pid = %d, want %d", got.pid, os.Getpid())
	}
}

// A reader who is also the operator should see their own instance, not the
// other privilege level.
func TestFindRunningInstancePrefersTheReadersOwnLock(t *testing.T) {
	dir := t.TempDir()
	mine := filepath.Join(dir, "mine.lock")
	other := filepath.Join(dir, "other.lock")
	if err := os.WriteFile(mine, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("4194304\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := findRunningInstanceAt(mine, other)
	if err != nil {
		t.Fatalf("findRunningInstanceAt: %v", err)
	}
	if got.level != "this user" {
		t.Fatalf("level = %q, want this user", got.level)
	}
}

// A root worker is the common case: the reader is unprivileged and still has
// to be told what is running and where its log is.
func TestFindRunningInstanceFindsARootInstance(t *testing.T) {
	dir := t.TempDir()
	rootLock := filepath.Join(dir, "root.lock")
	if err := os.WriteFile(rootLock, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := findRunningInstanceAt(filepath.Join(dir, "absent.lock"), rootLock)
	if err != nil {
		t.Fatalf("findRunningInstanceAt: %v", err)
	}
	if got.pid != os.Getpid() {
		t.Fatalf("pid = %d, want %d", got.pid, os.Getpid())
	}
	if got.log != logPathFor(0) {
		t.Fatalf("log = %q, want the root log %q", got.log, logPathFor(0))
	}
}

// The log grows for as long as the overlay is up, so status must not print all
// of it.
func TestTailFileReturnsTheLastLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ens.log")
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		b.WriteString("line" + strconv.Itoa(i) + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := tailFile(path, 3)
	if err != nil {
		t.Fatalf("tailFile: %v", err)
	}
	want := []string{"line18", "line19", "line20"}
	if len(lines) != len(want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("lines = %v, want %v", lines, want)
		}
	}
}

// Fewer lines than the limit is not an error and must not pad.
func TestTailFileKeepsAWholeShortLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ens.log")
	if err := os.WriteFile(path, []byte("only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := tailFile(path, 8)
	if err != nil {
		t.Fatalf("tailFile: %v", err)
	}
	if len(lines) != 1 || lines[0] != "only" {
		t.Fatalf("lines = %v, want [only]", lines)
	}
}

// The log may not exist yet, which is not a reason for status to fail.
func TestTailFileOnAMissingLog(t *testing.T) {
	if _, err := tailFile(filepath.Join(t.TempDir(), "absent.log"), 8); err == nil {
		t.Fatal("tailFile succeeded on a missing file")
	}
}

// The message has to carry the pid, or the reader cannot tell a live worker
// from a stale lock.
func TestRunStatusNamesTheProcess(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "ens.lock")
	log := filepath.Join(dir, "ens.log")
	if err := os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, []byte("ens: connected via quic\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := useStatusPaths(lock, log)
	defer restore()

	var out strings.Builder
	if err := runStatus(&out); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	got := out.String()
	for _, want := range []string{strconv.Itoa(os.Getpid()), log, "connected via quic"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status = %q, want it to name %q", got, want)
		}
	}
}

// The stop command is the thing a reader does next, so status has to print it
// rather than leave them to guess.
func TestRunStatusNamesTheStopCommand(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "ens.lock")
	if err := os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := useStatusPaths(lock, filepath.Join(dir, "absent.log"))
	defer restore()

	var out strings.Builder
	if err := runStatus(&out); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if !strings.Contains(out.String(), sudoCommand("down")) {
		t.Fatalf("status = %q, want it to name %q", out.String(), sudoCommand("down"))
	}
}

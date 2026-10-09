//go:build unix

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A reader who is told only "sudo ens" still types a command that a Homebrew
// install cannot resolve, so every hint has to name the running binary.
func TestSudoCommandNamesTheBinary(t *testing.T) {
	got := sudoCommand("down")
	if !strings.HasPrefix(got, "sudo ") {
		t.Fatalf("hint does not start with sudo: %q", got)
	}
	if !strings.HasSuffix(got, " down") {
		t.Fatalf("hint does not name the subcommand: %q", got)
	}
	if !strings.Contains(got, "ens") {
		t.Fatalf("hint does not name the binary: %q", got)
	}
	if strings.Contains(got, "sudo ens ") {
		t.Fatalf("hint still uses the bare name: %q", got)
	}
}

// sudo drops PATH, so the hint is useless unless the path is absolute.
func TestSudoCommandPathIsAbsolute(t *testing.T) {
	got := sudoCommand("invite", "--subnet", "10.99.0.0/24")
	fields := strings.Fields(got)
	if len(fields) < 2 {
		t.Fatalf("hint has no path: %q", got)
	}
	if !filepath.IsAbs(fields[1]) {
		t.Fatalf("hint path is not absolute: %q", got)
	}
	if !strings.Contains(got, "10.99.0.0/24") {
		t.Fatalf("hint dropped the arguments: %q", got)
	}
}

// Homebrew leaves a symlink in its bin directory and that link is the name a
// reader would type, so the hint keeps it rather than the versioned target.
func TestSudoCommandKeepsTheSymlinkName(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "ens")
	target := filepath.Join(dir, "Cellar", "ens", "0.2.16", "bin", "ens")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	old := os.Args[0]
	os.Args[0] = link
	defer func() { os.Args[0] = old }()

	got := sudoCommand("down")
	if !strings.Contains(got, link) {
		t.Fatalf("hint = %q, want it to name %q", got, link)
	}
}

// A bare "ens" carries no path, so the hint has to fall back to the running
// executable rather than print a name sudo cannot resolve.
func TestSudoCommandResolvesBareName(t *testing.T) {
	old := os.Args[0]
	os.Args[0] = "ens"
	defer func() { os.Args[0] = old }()

	got := sudoCommand("down")
	fields := strings.Fields(got)
	if len(fields) < 2 {
		t.Fatalf("hint has no path: %q", got)
	}
	if fields[1] == "ens" {
		t.Fatalf("hint kept the bare name: %q", got)
	}
	if !filepath.IsAbs(fields[1]) {
		t.Fatalf("hint path is not absolute: %q", got)
	}
}

func TestWrapStartNamesTheCommandWhenNotRoot(t *testing.T) {
	err := wrapStart(fmt.Errorf("TUNSETIFF enserie0: operation not permitted"))
	s := err.Error()
	if !strings.Contains(s, "need root") {
		t.Fatalf("error lost the need root note: %s", s)
	}
	if os.Geteuid() == 0 {
		return
	}
	fields := strings.Fields(s)
	var sawPath bool
	for _, f := range fields {
		if filepath.IsAbs(f) && strings.Contains(f, "ens") {
			sawPath = true
		}
	}
	if !sawPath {
		t.Fatalf("error does not name the binary to run: %s", s)
	}
	if !strings.Contains(s, "sudo") {
		t.Fatalf("error does not say how to gain root: %s", s)
	}
}

// The supervisor reads the worker's failure over a pipe that carries one line,
// so a hint with a newline loses every line after the first. The reader then
// sees the bare "need root" again, with no command to copy.
func TestWrapStartErrorStaysOnOneLine(t *testing.T) {
	err := wrapStart(fmt.Errorf("TUNSETIFF enserie0: operation not permitted"))
	if strings.ContainsAny(err.Error(), "\n") {
		t.Fatalf("error spans several lines, so the pipe keeps only the first: %q", err.Error())
	}
}

// Every hint the binary prints has to survive the same pipe.
func TestSudoHintsStayOnOneLine(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "ens.lock")
	if err := os.WriteFile(other, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{
		wrapStart(fmt.Errorf("TUNSETIFF enserie0: operation not permitted")),
		downError(errNotRunning, other),
	} {
		if err == nil {
			continue
		}
		if strings.ContainsAny(err.Error(), "\n") {
			t.Errorf("hint spans several lines: %q", err.Error())
		}
		if !strings.Contains(err.Error(), "sudo") {
			t.Errorf("hint does not name a sudo command: %q", err.Error())
		}
	}
}

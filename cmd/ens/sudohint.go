//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
)

// sudoCommand renders a command the reader can paste to reach this binary as
// root.
//
// A bare "ens" is not enough. Homebrew installs outside the directories in the
// sudo secure_path, so sudo reports "ens: command not found" and the reader is
// left with an instruction that cannot work. Naming the running binary gives
// one that works wherever Homebrew put it.
func sudoCommand(args ...string) string {
	name := selfPath()
	if name == "" {
		// Without a path there is nothing better to offer than the bare name,
		// so keep the instruction rather than drop it.
		return "sudo ens " + strings.Join(args, " ")
	}
	return "sudo " + name + " " + strings.Join(args, " ")
}

// selfPath returns the absolute path of the running binary, or an empty string
// when it cannot be read.
//
// os.Executable resolves the symlink a package manager leaves in its bin
// directory, so the result names the versioned file rather than the link. The
// link is what the reader would type, so prefer the name this process was
// started with when that name is already a path.
func selfPath() string {
	if len(os.Args) > 0 && strings.ContainsRune(os.Args[0], filepath.Separator) {
		if abs, err := filepath.Abs(os.Args[0]); err == nil {
			return abs
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if abs, err := filepath.Abs(exe); err == nil {
		return abs
	}
	return exe
}

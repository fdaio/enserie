package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The lock and the log must land in a directory that exists. On macOS there
// is no /run, so a hardcoded /run/ens.lock fails before any TUN work starts,
// which is what a user on macOS hit.
func TestRuntimeDirExists(t *testing.T) {
	dir := runtimeDir()
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("runtime dir %s: %v", dir, err)
	}
	if !st.IsDir() {
		t.Fatalf("runtime dir %s is not a directory", dir)
	}
}

// Root needs the lock to be somewhere another user cannot pre-create, because
// a lock file that already exists is trusted by flock.
func TestRuntimeDirIsNotWorldWritable(t *testing.T) {
	st, err := os.Stat(runtimeDir())
	if err != nil {
		t.Fatalf("stat runtime dir: %v", err)
	}
	if st.Mode().Perm()&0o002 != 0 {
		t.Errorf("runtime dir %s is world writable (%v)", runtimeDir(), st.Mode().Perm())
	}
}

// The paths a root run would use have to sit in a directory that exists on
// this platform. Checking the root branch on any runner is the point: macOS
// has no /run, and a test that only ran as root would never run on the macOS
// CI runner at all.
func TestRootPathsPointAtAnExistingDirectory(t *testing.T) {
	for name, path := range map[string]string{
		"lock": lockPathFor(0),
		"log":  logPathFor(0),
	} {
		st, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Errorf("root %s path %s: parent directory missing: %v", name, path, err)
			continue
		}
		if !st.IsDir() {
			t.Errorf("root %s path %s: parent is not a directory", name, path)
		}
	}
}

// A running instance must block a second one.
func TestInstanceLockIsExclusive(t *testing.T) {
	dir := t.TempDir()
	first, err := acquireInstanceLockAt(filepath.Join(dir, "ens.lock"))
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	defer func() { _ = first.Close() }()

	if _, err := acquireInstanceLockAt(filepath.Join(dir, "ens.lock")); err == nil {
		t.Fatal("a second lock was granted while the first was held")
	}
}

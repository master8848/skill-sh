//go:build windows

package cache

import (
	"os"
	"path/filepath"
)

// withLock is a no-op lock stub for Windows.
// syscall.Flock is not available on Windows (GOOS=windows). Proper Windows file
// locking would use golang.org/x/sys/windows.LockFileEx, but a no-op that
// still executes fn() is sufficient to unblock `GOOS=windows go build` and
// avoids deadlocks. Concurrent cache access on Windows will be racy but not
// corrupt beyond what the existing git operations tolerate.
//
// If stronger locking is needed later, replace this with LockFileEx-based
// implementation without changing callers.
func withLock(cachePath string, fn func() error) error {
	lockPath := filepath.Join(cachePath, ".lock")
	if err := os.MkdirAll(cachePath, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	// No file locking on Windows — execute directly.
	return fn()
}

// tryLock is a no-op stub for Windows. It always succeeds and returns a
// release func that simply closes the lock file. This preserves GC semantics
// (caller checks err == nil to decide whether to delete) while allowing the
// package to compile on GOOS=windows.
func tryLock(cachePath string) (func(), error) {
	lockPath := filepath.Join(cachePath, ".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	release := func() {
		f.Close()
	}
	return release, nil
}

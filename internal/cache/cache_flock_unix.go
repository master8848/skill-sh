//go:build !windows

package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func withLock(cachePath string, fn func() error) error {
	lockPath := filepath.Join(cachePath, ".lock")
	// Ensure parent exists. For clone case cachePath may not exist; create it (empty) so lock file can be created.
	if err := os.MkdirAll(cachePath, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	deadline := time.Now().Add(30 * time.Second)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout acquiring lock %s: %w", lockPath, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return fn()
}

func tryLock(cachePath string) (func(), error) {
	lockPath := filepath.Join(cachePath, ".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	release := func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
		f.Close()
	}
	return release, nil
}

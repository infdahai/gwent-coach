//go:build darwin || linux

package main

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Keep the lock file in place: unlinking it can split concurrent lockers across inodes.
func acquireImportLock(dest string) (func(), error) {
	fd, err := unix.Open(filepath.Join(dest, ".collection-import.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "collection-import-lock")
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, err
	}
	return func() { file.Close() }, nil
}

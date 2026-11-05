//go:build !windows

package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// SyncDir syncs a directory, so that entries created, renamed or removed in it
// survive power loss. Filesystems that can't sync a directory are treated as
// success, because they offer nothing stronger.
func SyncDir(dir string) error {
	err := syncOpen(dir, os.O_RDONLY)
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
		return nil
	}
	return err
}

// On POSIX, syncing the directory covers the new name.
func syncLink(string) error { return nil }

func replace(tmp, path string) error { return os.Rename(tmp, path) }

// publishNew links tmp to path, which fails if path exists, then drops tmp's name.
// The directory sync that follows covers both changes.
func publishNew(tmp, path string) error {
	if err := os.Link(tmp, path); err != nil {
		return err
	}
	return os.Remove(tmp)
}

func syncRenamed(path string) error { return SyncDir(filepath.Dir(path)) }

func tryLock(f *os.File) (bool, error) {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK):
			return false, nil
		default:
			return false, err
		}
	}
}

func unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

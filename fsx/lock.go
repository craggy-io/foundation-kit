package fsx

import (
	"errors"
	"io/fs"
	"os"
)

// ErrLocked is returned, wrapped in an *fs.PathError, when another holder has the
// lock.
var ErrLocked = errors.New("fsx: locked by another holder")

// Lock is an exclusive lock on a file, held until Unlock or until the process exits.
// A Lock isn't safe for concurrent use.
type Lock struct {
	f *os.File
}

// TryLock takes an exclusive lock on path without waiting, creating the file with
// mode 0644 if it doesn't exist. If another holder has the lock, in this process or
// another, it fails with an error that matches ErrLocked.
//
// The lock file is never deleted, because deleting it would let a later TryLock lock
// a different file while the first holder still runs.
func TryLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	ok, err := tryLock(f)
	if err == nil && !ok {
		err = ErrLocked
	}
	if err != nil {
		_ = f.Close()
		return nil, &fs.PathError{Op: "lock", Path: path, Err: err}
	}
	return &Lock{f: f}, nil
}

// File returns the locked file, for example to record who holds the lock. Other
// processes can still read it, so they can report the holder when TryLock fails.
func (l *Lock) File() *os.File { return l.f }

// Unlock releases the lock and closes the file. Calling it again returns an error
// that matches os.ErrClosed.
func (l *Lock) Unlock() error {
	if l.f == nil {
		return os.ErrClosed
	}
	f := l.f
	l.f = nil
	return errors.Join(unlock(f), f.Close())
}

//go:build windows

package fsx

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procMoveFileExW  = kernel32.NewProc("MoveFileExW")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8

	lockExclusive      = 0x2
	lockFailImmediate  = 0x1
	errorLockViolation = syscall.Errno(33)
	errorIOPending     = syscall.Errno(997)
	// The lock covers one byte far past any content, because Windows locks are
	// mandatory and would otherwise stop other processes reading the lock file.
	lockOffsetHigh = 0x7fff0000
)

// SyncDir does nothing on Windows: directory handles can't be flushed, and NTFS
// journals namespace changes. The write functions rename with write-through, and
// SyncEntry flushes through the new name instead.
func SyncDir(string) error { return nil }

// syncLink flushes through the new name. FlushFileBuffers commits the NTFS log up to
// this file's latest metadata change, which includes creating the name.
func syncLink(path string) error { return syncOpen(path, os.O_RDWR) }

func replace(tmp, path string) error {
	return moveFile(tmp, path, moveFileReplaceExisting|moveFileWriteThrough)
}

// Without MOVEFILE_REPLACE_EXISTING the move fails if path exists, with an error
// that matches fs.ErrExist.
func publishNew(tmp, path string) error { return moveFile(tmp, path, moveFileWriteThrough) }

// Write-through renames are durable when they return.
func syncRenamed(string) error { return nil }

func moveFile(from, to string, flags uintptr) error {
	f, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	t, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	r, _, callErr := procMoveFileExW.Call(uintptr(unsafe.Pointer(f)), uintptr(unsafe.Pointer(t)), flags)
	if r == 0 {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: callErr}
	}
	return nil
}

func tryLock(f *os.File) (bool, error) {
	overlapped := syscall.Overlapped{OffsetHigh: lockOffsetHigh}
	r, _, err := procLockFileEx.Call(f.Fd(), lockExclusive|lockFailImmediate, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if r != 0 {
		return true, nil
	}
	if errors.Is(err, errorLockViolation) || errors.Is(err, errorIOPending) {
		return false, nil
	}
	return false, err
}

func unlock(f *os.File) error {
	overlapped := syscall.Overlapped{OffsetHigh: lockOffsetHigh}
	r, _, err := procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if r != 0 {
		return nil
	}
	return err
}

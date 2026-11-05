// Package fsx writes files so that a crash or power loss leaves either the old
// content or the new, never a mixture, and takes exclusive locks on files.
//
// WriteFile and Write replace a file through a temporary file in the same directory:
// the content is synced, renamed over the target, and the rename itself is made
// durable before they return. WriteNew does the same but never replaces an existing
// file. SyncEntry makes a name you created by other means durable, such as a hard
// link.
//
// # Invariants
//
//   - A reader of path sees the complete old content or the complete new content.
//   - When a write returns nil, the new content survives a crash or power loss on a
//     local filesystem.
//   - When a write fails, path is unchanged and no temporary file is left behind.
//   - perm is applied exactly, without the process umask, so the result doesn't
//     depend on who runs the program.
//   - The parent directory must already exist. These functions never create
//     directories, so a mistyped path fails instead of growing a tree.
//
// A Lock is held by an open file handle, so the operating system releases it when
// the holder exits or crashes, and a stale lock file never blocks a later run. Two
// TryLock calls on one path conflict even inside one process.
//
// # Concurrency
//
// The write functions are safe to call concurrently, including on one path: each
// writer gets its own temporary file, and the last rename wins. A Lock isn't safe
// for concurrent use; share the lock, not the *Lock.
//
// # Exported names
//
//	WriteFile, Write          atomically replace a file
//	WriteNew                  atomically create a file that must not exist yet
//	SyncDir, SyncEntry        make namespace changes durable
//	Lock, TryLock             exclusive, crash-safe file locks
//	ErrLocked                 another holder has the lock
package fsx

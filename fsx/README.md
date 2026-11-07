# fsx

`fsx` writes files so that a crash or power loss leaves either the old content or the new, never a mixture, and takes exclusive locks that the operating system releases when the holder dies.

```go
import "github.com/craggy-io/foundation-kit/fsx"
```

It uses only the standard library.

## Writing a file

```go
// Replace a file atomically. perm is applied exactly, without the umask.
err := fsx.WriteFile("var/state.json", data, 0o644)

// Stream into it instead. If the callback fails, the old file is untouched.
err = fsx.Write("var/journal", 0o600, func(w io.Writer) error {
	return json.NewEncoder(w).Encode(records)
})

// Create a file that must not exist yet. Of several racing writers, exactly one wins.
err = fsx.WriteNew("cache/ab/abcdef", data, 0o444)
if errors.Is(err, fs.ErrExist) {
	// someone else wrote it first
}
```

Each write goes to a temporary file in the same directory, is synced, renamed over the target, and the rename is synced too, before the function returns. On failure the temporary file is removed. The parent directory must exist; `fsx` never creates directories.

If you publish a name yourself, for example with `os.Link`, call `fsx.SyncEntry(path, stop)` before reporting it durable. `fsx.SyncDir` syncs one directory.

## Locking

```go
lock, err := fsx.TryLock(filepath.Join(dir, "owner.lock"))
if errors.Is(err, fsx.ErrLocked) {
	return fmt.Errorf("%s is in use by another process", dir)
}
defer lock.Unlock()
```

`TryLock` never waits. The lock belongs to the open file handle, so a crashed holder never leaves a stale lock, and two `TryLock` calls in one process conflict too. The lock file stays readable while it's held, so a holder can write its identity into `lock.File()` for others to report.

## Platforms

| | Unix | Windows |
| --- | --- | --- |
| Replace | `rename`, then fsync the directory | `MoveFileEx` with write-through |
| Create only | hard link, then fsync the directory | `MoveFileEx` without replace, with write-through |
| Lock | `flock` | `LockFileEx` on one byte far past the content |

`WriteNew` on Unix needs a filesystem with hard links.

## Implementation status

- Working: `WriteFile`, `Write`, `WriteNew`, `SyncDir`, `SyncEntry`, `TryLock`.
- Windows code is vetted by cross-compiling, but its tests haven't been run on Windows yet.

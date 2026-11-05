package fsx

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"unicode/utf8"
)

// WriteFile atomically replaces path with data, with mode perm. See Write.
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	return Write(path, perm, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// Write atomically replaces path with what write produces, with mode perm. write
// receives a temporary file in path's directory. If write returns an error, path is
// left unchanged and the error is returned as it is.
//
// When Write returns nil, both the new content and the rename that published it are
// synced to stable storage.
func Write(path string, perm fs.FileMode, write func(io.Writer) error) error {
	tmp, err := stage(path, perm, write)
	if err != nil {
		return err
	}
	if err := replace(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncRenamed(path)
}

// WriteNew atomically creates path with data, with mode perm, and fails if path
// already exists. The error then satisfies errors.Is(err, fs.ErrExist), and the
// existing file is untouched. When several writers race to create one path, exactly
// one of them succeeds.
//
// On Unix it publishes the file with a hard link, so the filesystem must support
// them.
func WriteNew(path string, data []byte, perm fs.FileMode) error {
	tmp, err := stage(path, perm, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
	if err != nil {
		return err
	}
	if err := publishNew(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncRenamed(path)
}

// stage writes a synced, closed temporary file beside path and returns its name.
func stage(path string, perm fs.FileMode, write func(io.Writer) error) (_ string, err error) {
	base := filepath.Base(path)
	if len(base) > 32 { // keep the temporary name within NAME_MAX
		cut := 32
		for cut > 0 && !utf8.RuneStart(base[cut]) {
			cut--
		}
		base = base[:cut]
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+base+".tmp-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	defer func() {
		if err != nil {
			if f != nil {
				_ = f.Close()
			}
			_ = os.Remove(name)
		}
	}()
	if err = f.Chmod(perm); err != nil {
		return "", err
	}
	if err = write(f); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	err = f.Close()
	f = nil
	if err != nil {
		return "", err
	}
	return name, nil
}

// SyncEntry makes durable the namespace change that made path visible, such as a
// hard link or rename you made yourself, plus every parent directory up to and
// including stop. An empty stop syncs only path's own directory. stop must be path's
// directory or one of its ancestors, written the same way.
//
// Call it after publishing a name and before reporting that the name is durable. The
// write functions in this package already do this for their own path.
func SyncEntry(path, stop string) error {
	if err := syncLink(path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	for {
		if err := SyncDir(dir); err != nil {
			return err
		}
		parent := filepath.Dir(dir)
		if stop == "" || dir == stop || parent == dir {
			return nil
		}
		dir = parent
	}
}

func syncOpen(path string, flag int) error {
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

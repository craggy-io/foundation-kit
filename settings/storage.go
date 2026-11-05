package settings

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Storage keeps a Store's stored values as one document. It lets a host keep them
// somewhere other than a file, such as a database, and lets a test simulate a failure.
type Storage interface {
	// Read returns the last document written. It returns an error matching
	// fs.ErrNotExist if nothing has been written yet.
	Read(ctx context.Context) ([]byte, error)

	// Write replaces the document. It must either replace all of it or leave the old
	// one in place, so that a crash can't lose what the user set.
	Write(ctx context.Context, data []byte) error
}

// File returns a Storage that keeps the document in the file at path. It creates the
// file and its parent directories when first written, and replaces the file atomically.
// A new file is readable only by its owner where the platform supports that, because it
// may hold secrets in plain text.
func File(path string) Storage { return fileStorage{path: path} }

type fileStorage struct{ path string }

func (f fileStorage) Read(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.ReadFile(f.path)
}

func (f fileStorage) Write(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(f.path)+".*")
	if err != nil {
		return err
	}
	_, writeErr := tmp.Write(data)
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("writing %s: %w", f.path, err)
	}
	if err := os.Rename(tmp.Name(), f.path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

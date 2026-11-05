package fsx_test

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/craggy-io/foundation-kit/fsx"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// onlyEntries fails if dir holds anything but names, such as a leftover temp file.
func onlyEntries(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if len(got) != len(names) {
		t.Fatalf("entries = %q, want %q", got, names)
	}
	for i := range names {
		if got[i] != names[i] {
			t.Fatalf("entries = %q, want %q", got, names)
		}
	}
}

func TestWriteFileCreatesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := fsx.WriteFile(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fsx.WriteFile(path, []byte("two"), 0o640); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "two" {
		t.Fatalf("content = %q", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o640 {
			t.Fatalf("perm = %v, want exact 0640 regardless of umask", info.Mode().Perm())
		}
	}
	onlyEntries(t, dir, "state.json")
}

func TestWriteFailureLeavesOldContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "journal")
	if err := fsx.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := fsx.Write(path, 0o644, func(w io.Writer) error {
		if _, err := w.Write([]byte("partial")); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the callback's error", err)
	}
	if got := read(t, path); got != "old" {
		t.Fatalf("content = %q, want old", got)
	}
	onlyEntries(t, dir, "journal")
}

func TestWriteNeedsParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "file")
	if err := fsx.WriteFile(path, nil, 0o644); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestWriteLongName(t *testing.T) {
	dir := t.TempDir()
	name := string(make([]byte, 0, 200))
	for len(name) < 200 {
		name += "x"
	}
	if err := fsx.WriteFile(filepath.Join(dir, name), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWriteNewNeverReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entry")
	if err := fsx.WriteNew(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := fsx.WriteNew(path, []byte("second"), 0o644)
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	if got := read(t, path); got != "first" {
		t.Fatalf("content = %q, want first", got)
	}
	onlyEntries(t, dir, "entry")
}

func TestWriteNewRaceHasOneWinner(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entry")
	const writers = 16
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = fsx.WriteNew(path, []byte{byte('a' + i)}, 0o644)
		}()
	}
	wg.Wait()
	winners := 0
	for _, err := range errs {
		switch {
		case err == nil:
			winners++
		case !errors.Is(err, fs.ErrExist):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want 1", winners)
	}
	onlyEntries(t, dir, "entry")
}

func TestSyncEntry(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "src")
	if err := fsx.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Link(src, link); err != nil {
		t.Skip("no hard links:", err)
	}
	if err := fsx.SyncEntry(link, root); err != nil {
		t.Fatal(err)
	}
	if err := fsx.SyncEntry(link, ""); err != nil {
		t.Fatal(err)
	}
}

func TestLockExcludesOtherHolders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.lock")
	first, err := fsx.TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fsx.TryLock(path); !errors.Is(err, fsx.ErrLocked) {
		t.Fatalf("second TryLock err = %v, want ErrLocked", err)
	}
	if _, err := first.File().WriteString("pid 1"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "pid 1" {
		t.Fatalf("lock file isn't readable while held: %q", got)
	}
	if err := first.Unlock(); err != nil {
		t.Fatal(err)
	}
	if err := first.Unlock(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("second Unlock err = %v, want os.ErrClosed", err)
	}
	again, err := fsx.TryLock(path)
	if err != nil {
		t.Fatalf("TryLock after Unlock: %v", err)
	}
	if err := again.Unlock(); err != nil {
		t.Fatal(err)
	}
}

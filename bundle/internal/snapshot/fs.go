package snapshot

import (
	"bytes"
	"io"
	"io/fs"
	"path"
	"slices"
	"time"
)

// snapshotFS is a read-only fs.FS over a snapshot's files, with their directories
// implied by their paths. It is safe for concurrent use because nothing in it changes.
type snapshotFS struct {
	files map[string][]byte
	dirs  map[string][]string // directory to its sorted child names
}

var (
	_ fs.ReadFileFS = (*snapshotFS)(nil)
	_ fs.ReadDirFS  = (*snapshotFS)(nil)
	_ fs.StatFS     = (*snapshotFS)(nil)
)

func (s *snapshotFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if data, ok := s.files[name]; ok {
		return &file{info: fileInfo{name: path.Base(name), size: int64(len(data))}, r: bytes.NewReader(data)}, nil
	}
	if _, ok := s.dirs[name]; ok {
		entries, _ := s.ReadDir(name)
		return &dir{info: fileInfo{name: path.Base(name), dir: true}, entries: entries}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func (s *snapshotFS) ReadFile(name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readfile", Path: name, Err: fs.ErrInvalid}
	}
	data, ok := s.files[name]
	if !ok {
		if _, isDir := s.dirs[name]; isDir {
			return nil, &fs.PathError{Op: "readfile", Path: name, Err: fs.ErrInvalid}
		}
		return nil, &fs.PathError{Op: "readfile", Path: name, Err: fs.ErrNotExist}
	}
	return slices.Clone(data), nil
}

func (s *snapshotFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	names, ok := s.dirs[name]
	if !ok {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	entries := make([]fs.DirEntry, 0, len(names))
	for _, n := range names {
		full := path.Join(name, n)
		if data, isFile := s.files[full]; isFile {
			entries = append(entries, fs.FileInfoToDirEntry(fileInfo{name: n, size: int64(len(data))}))
		} else {
			entries = append(entries, fs.FileInfoToDirEntry(fileInfo{name: n, dir: true}))
		}
	}
	return entries, nil
}

func (s *snapshotFS) Stat(name string) (fs.FileInfo, error) {
	f, err := s.Open(name)
	if err != nil {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: err.(*fs.PathError).Err}
	}
	defer f.Close()
	return f.Stat()
}

// fileInfo describes a snapshotted file or directory. Snapshots record no times or
// permissions, so every file reads as 0o444 and every directory as 0o555.
type fileInfo struct {
	name string
	size int64
	dir  bool
}

func (fi fileInfo) Name() string { return fi.name }
func (fi fileInfo) Size() int64  { return fi.size }
func (fi fileInfo) Mode() fs.FileMode {
	if fi.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (fi fileInfo) ModTime() time.Time { return time.Time{} }
func (fi fileInfo) IsDir() bool        { return fi.dir }
func (fi fileInfo) Sys() any           { return nil }

type file struct {
	info fileInfo
	r    *bytes.Reader
}

func (f *file) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *file) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *file) Close() error               { return nil }

// ReadAt and Seek let callers such as http.FileServer use a snapshotted file.
func (f *file) ReadAt(p []byte, off int64) (int, error)   { return f.r.ReadAt(p, off) }
func (f *file) Seek(off int64, whence int) (int64, error) { return f.r.Seek(off, whence) }

type dir struct {
	info    fileInfo
	entries []fs.DirEntry
	offset  int
}

func (d *dir) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *dir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.info.name, Err: fs.ErrInvalid}
}
func (d *dir) Close() error { return nil }

func (d *dir) ReadDir(n int) ([]fs.DirEntry, error) {
	rest := d.entries[d.offset:]
	if n <= 0 {
		d.offset = len(d.entries)
		return slices.Clone(rest), nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	if n > len(rest) {
		n = len(rest)
	}
	d.offset += n
	return slices.Clone(rest[:n]), nil
}

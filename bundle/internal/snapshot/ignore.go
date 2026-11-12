package snapshot

import (
	"io/fs"
	"path"
	"strings"
)

// ignoredNames are the file and directory names that are never part of a
// bundle, at any depth: version-control folders, tool caches and files that
// editors and operating systems leave behind. They appear in a bundle's folder
// while someone works on it, and none of them is content an author ships.
//
// The list is fixed rather than configured by a file in the bundle, because such
// a file would itself decide what is hashed. Add a name only when a tool writes
// it into a source folder without being asked.
var ignoredNames = map[string]bool{
	// Version control.
	".git": true, ".hg": true, ".svn": true,
	// Dependency folders and tool caches.
	"node_modules": true, ".venv": true, "__pycache__": true,
	".pytest_cache": true, ".mypy_cache": true, ".ruff_cache": true, ".dart_tool": true,
	// Editors.
	".idea": true, ".vscode": true,
	// Operating systems.
	".DS_Store": true, "Thumbs.db": true, "desktop.ini": true,
}

// Ignored reports whether name, one path element, is never part of a bundle.
func Ignored(name string) bool { return ignoredNames[name] }

// ignoredPath reports whether any element of the slash-separated path p is
// ignored.
func ignoredPath(p string) bool {
	for _, elem := range strings.Split(p, "/") {
		if Ignored(elem) {
			return true
		}
	}
	return false
}

// Visible returns a view of fsys without ignored files and directories: they
// aren't listed, and opening one reports fs.ErrNotExist. Everything that reads a
// bundle's files goes through it, so an ignored file is never hashed, counted,
// compiled, copied by an install or read back from an installed bundle.
func Visible(fsys fs.FS) fs.FS { return visibleFS{fsys} }

type visibleFS struct{ fsys fs.FS }

func notExist(op, name string) error {
	return &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
}

func (v visibleFS) Open(name string) (fs.File, error) {
	if ignoredPath(name) {
		return nil, notExist("open", name)
	}
	f, err := v.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	// A directory opened directly must filter its listing too.
	if dir, ok := f.(fs.ReadDirFile); ok {
		if info, err := f.Stat(); err == nil && info.IsDir() {
			return visibleDir{dir}, nil
		}
	}
	return f, nil
}

func (v visibleFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if ignoredPath(name) {
		return nil, notExist("readdir", name)
	}
	entries, err := fs.ReadDir(v.fsys, name)
	return visibleEntries(entries), err
}

// ReadLink and Lstat pass through, so a symlink is still seen as one and
// rejected by Take rather than hidden.
func (v visibleFS) ReadLink(name string) (string, error) {
	if ignoredPath(name) {
		return "", notExist("readlink", name)
	}
	return fs.ReadLink(v.fsys, name)
}

func (v visibleFS) Lstat(name string) (fs.FileInfo, error) {
	if ignoredPath(name) {
		return nil, notExist("lstat", name)
	}
	return fs.Lstat(v.fsys, name)
}

type visibleDir struct{ fs.ReadDirFile }

func (d visibleDir) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, err := d.ReadDirFile.ReadDir(n)
	return visibleEntries(entries), err
}

func visibleEntries(entries []fs.DirEntry) []fs.DirEntry {
	kept := entries[:0:0]
	for _, e := range entries {
		if !Ignored(path.Base(e.Name())) {
			kept = append(kept, e)
		}
	}
	return kept
}

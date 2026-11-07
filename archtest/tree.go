package archtest

import (
	"fmt"
	"go/parser"
	"go/scanner"
	"go/token"
	"strconv"
	"strings"
)

// checkTree checks one selected directory's immediate children, and its child
// directories' contents.
func (r *repo) checkTree(t *treeCheck, dir *entry) []diagnostic {
	var ds []diagnostic
	add := func(code, path, format string, args ...any) {
		ds = append(ds, diagnostic{Code: code, Path: path, Message: fmt.Sprintf(format, args...)})
	}
	children := r.children[dir.path]

	// requireChildren reports each pattern that no child of the right kind matches.
	requireChildren := func(in string, entries []*entry, patterns []string, k entryKind) {
		for _, p := range patterns {
			if hasChild(entries, p, k) {
				continue
			}
			expected := joinPath(in, p)
			if isLiteral(p) && r.isExcluded(expected) {
				add(codeConfigConflict, expected, "the required %s is excluded at the root, so the requirement can't be checked", k)
				continue
			}
			add(codeEntryMissing, expected, "required %s %q is missing", k, p)
		}
	}
	requireChildren(dir.path, children, t.RequireFiles, entryFile)
	requireChildren(dir.path, children, t.RequireDirs, entryDir)

	for _, c := range children {
		switch {
		case matchAnyName(t.Forbid, c.name):
			add(codeEntryForbidden, c.path, "%s %q is forbidden here", c.kind, c.name)
		case t.Allow && c.kind == entryFile && !matchAnyName(t.AllowFiles, c.name):
			add(codeEntryUnexpected, c.path, "file %q isn't in this directory's allow list; move it, or add it to the list", c.name)
		case t.Allow && c.kind == entryDir && !matchAnyName(t.AllowDirs, c.name):
			add(codeEntryUnexpected, c.path, "directory %q isn't in this directory's allow list; move it, or add it to the list", c.name)
		}
	}

	if cc := t.Children; cc != nil {
		var dirs []*entry
		for _, c := range children {
			if c.kind == entryDir {
				dirs = append(dirs, c)
			}
		}
		if len(dirs) < cc.Min {
			add(codeChildCount, dir.path, "has %d child directories, want at least %d", len(dirs), cc.Min)
		}
		for _, c := range dirs {
			requireChildren(c.path, r.children[c.path], cc.RequireFiles, entryFile)
			if len(cc.ForbidDescendants) == 0 {
				continue
			}
			r.descendants(c.path, func(e *entry) {
				if matchAnyPath(cc.ForbidDescendants, strings.TrimPrefix(e.path, c.path+"/")) {
					add(codeEntryForbidden, e.path, "%s %q is forbidden inside %s", e.kind, e.name, c.path)
				}
			})
		}
	}
	return ds
}

func hasChild(entries []*entry, pattern string, k entryKind) bool {
	for _, e := range entries {
		if e.kind == k && matchName(pattern, e.name) {
			return true
		}
	}
	return false
}

// descendants calls fn for every entry below dir, in sorted order.
func (r *repo) descendants(dir string, fn func(*entry)) {
	for _, c := range r.children[dir] {
		fn(c)
		if c.kind == entryDir {
			r.descendants(c.path, fn)
		}
	}
}

// checkGoSource checks one Go file's imports and directive comments. Unlike import
// rules it reads exactly the files selected, including tests, generated files, nested
// Go modules and files behind any build constraint.
func (r *repo) checkGoSource(g *goSourceCheck, e *entry) []diagnostic {
	data, ok := r.readFile(e)
	if !ok {
		return nil
	}
	var ds []diagnostic
	fset := token.NewFileSet()
	at := func(p token.Pos, code, format string, args ...any) {
		position := fset.Position(p)
		ds = append(ds, diagnostic{Code: code, Path: e.path, Line: position.Line, Column: position.Column,
			Message: fmt.Sprintf(format, args...)})
	}

	if len(g.ForbidImports) > 0 {
		f, err := parser.ParseFile(fset, e.path, data, parser.ImportsOnly)
		if err != nil {
			ds = append(ds, diagnostic{Code: codeParseFailed, Path: e.path, Message: fmt.Sprintf("can't read imports: %v", err)})
		}
		if f != nil {
			for _, spec := range f.Imports {
				imp, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					continue
				}
				for _, forbid := range g.ForbidImports {
					if imp == forbid || (strings.HasSuffix(forbid, "/...") && under(imp, strings.TrimSuffix(forbid, "/..."))) {
						how := ""
						if spec.Name != nil {
							how = " as " + spec.Name.Name
						}
						at(spec.Pos(), codeImportForbidden, "imports %q%s", imp, how)
						break
					}
				}
			}
		}
	}

	if len(g.ForbidDirectives) > 0 {
		// Scan real comment tokens, so a directive-shaped string isn't mistaken for
		// one. A misplaced or malformed directive still counts.
		var s scanner.Scanner
		s.Init(fset.AddFile(e.path, -1, len(data)), data, func(token.Position, string) {}, scanner.ScanComments)
		for {
			p, tok, lit := s.Scan()
			if tok == token.EOF {
				break
			}
			if tok != token.COMMENT || !strings.HasPrefix(lit, "//") {
				continue
			}
			body := lit[2:]
			for _, name := range g.ForbidDirectives {
				if rest, ok := strings.CutPrefix(body, name); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
					at(p, codeDirective, "has a //%s directive", name)
				}
			}
		}
	}
	return ds
}

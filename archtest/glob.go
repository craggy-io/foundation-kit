package archtest

import (
	"fmt"
	"strings"
)

// Paths and globs in archtest.yaml are relative to the repository root, use "/", and
// are case-sensitive on every OS. "." alone is the root.
//
// A glob supports "*" (zero or more characters within one component), "?" (one
// character) and "**" (zero or more whole components). Nothing else is special.

// checkPath reports why p can't be a repository-relative path or path glob. With
// glob false, wildcards are rejected too. With component true, p must be a single
// name glob, as in a tree rule's child lists.
func checkPath(p string, glob, component bool) error {
	switch {
	case p == "":
		return fmt.Errorf("is empty")
	case p == ".":
		if component {
			return fmt.Errorf("%q isn't a name", p)
		}
		return nil
	case strings.Contains(p, `\`):
		return fmt.Errorf("%q uses a backslash; use /", p)
	case strings.HasPrefix(p, "/"), len(p) >= 2 && p[1] == ':':
		return fmt.Errorf("%q is absolute; paths are relative to the repository root", p)
	case strings.HasPrefix(p, "./"):
		return fmt.Errorf("%q starts with ./; write it without", p)
	case strings.ContainsAny(p, "[]{}"):
		return fmt.Errorf("%q uses brackets or braces, which globs don't support", p)
	case strings.HasPrefix(p, "!"):
		return fmt.Errorf("%q starts with !; globs have no negation, so use exclude", p)
	}
	parts := strings.Split(p, "/")
	if component && len(parts) > 1 {
		return fmt.Errorf("%q has a /; this list names immediate children only", p)
	}
	for _, part := range parts {
		switch {
		case part == "":
			return fmt.Errorf("%q has an empty component", p)
		case part == "." || part == "..":
			return fmt.Errorf("%q has a %q component", p, part)
		case !glob && strings.ContainsAny(part, "*?"):
			return fmt.Errorf("%q must be a literal path, without wildcards", p)
		case strings.Contains(part, "**") && part != "**":
			return fmt.Errorf("%q uses ** inside a component; ** must be a whole component", p)
		case component && part == "**":
			return fmt.Errorf("%q uses **; this list names immediate children only", p)
		}
	}
	return nil
}

func isLiteral(p string) bool { return !strings.ContainsAny(p, "*?") }

// matchPath reports whether path matches the glob pattern. Both have been checked.
func matchPath(pattern, path string) bool {
	if pattern == "." || path == "." {
		return pattern == path
	}
	return matchParts(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func matchParts(pattern, path []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			for i := 0; i <= len(path); i++ {
				if matchParts(pattern[1:], path[i:]) {
					return true
				}
			}
			return false
		}
		if len(path) == 0 || !matchName(pattern[0], path[0]) {
			return false
		}
		pattern, path = pattern[1:], path[1:]
	}
	return len(path) == 0
}

// matchName matches one component against a pattern of literal text, "*" and "?".
func matchName(pattern, name string) bool {
	p, n := []rune(pattern), []rune(name)
	// Classic iterative wildcard match with one backtrack point.
	pi, ni, star, mark := 0, 0, -1, 0
	for ni < len(n) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == n[ni]):
			pi++
			ni++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, ni
			pi++
		case star >= 0:
			pi, mark = star+1, mark+1
			ni = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

func matchAnyName(patterns []string, name string) bool {
	for _, p := range patterns {
		if matchName(p, name) {
			return true
		}
	}
	return false
}

func matchAnyPath(patterns []string, path string) bool {
	for _, p := range patterns {
		if matchPath(p, path) {
			return true
		}
	}
	return false
}

// joinPath joins a repository-relative directory and a relative path.
func joinPath(dir, rel string) string {
	if dir == "." {
		return rel
	}
	return dir + "/" + rel
}

package archtest

import (
	"bufio"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Patterns that match by origin rather than by path, for use in Rule.Forbid and
// Rule.Allow.
const (
	// Std matches the standard library: any path outside this Go module whose first
	// element has no dot.
	Std = "std"

	// External matches every other Go module: any path outside this Go module whose
	// first element has a dot.
	External = "external"
)

// Rule forbids a set of imports from a set of packages.
//
// A pattern is written the way the go command writes one: "./kind" is that package,
// "./bundle/..." is that package and everything below it, and "." is the root package
// of the Go module. A full import path works too, so "net/http" and
// "github.com/x/y/..." can be forbidden. Std and External are also accepted in Forbid
// and Allow.
type Rule struct {
	// Name identifies the rule in a failure. It is required.
	Name string

	// Why tells the reader what to do instead. It is required, because a failure that
	// doesn't explain itself invites someone to weaken the rule.
	Why string

	// From lists the packages the rule applies to. It is required, and may only name
	// packages in this Go module.
	From []string

	// Except removes packages from From.
	Except []string

	// Forbid lists the imports those packages must not have. It is required.
	Forbid []string

	// Allow removes imports from Forbid.
	Allow []string
}

// Config says what Check examines. The zero value checks only that go.mod has no
// replace directives, which isn't useful on its own, so a Config normally has Rules.
type Config struct {
	// Dir is the directory holding go.mod. If it is empty, Check uses the nearest
	// go.mod at or above the test's working directory.
	Dir string

	// Rules are the import rules to enforce.
	Rules []Rule

	// Packages, if it isn't empty, lists every package the rules are expected to
	// cover, using the patterns described on Rule. A package it doesn't match is a
	// failure, so a new package can't escape the rules by being left out of them.
	// Packages containing only test files are exempt.
	Packages []string

	// AllowReplace turns off the check that go.mod has no replace directives. Use a
	// go.work file for local development rather than setting this.
	AllowReplace bool
}

// Check fails t if the Go module breaks any rule, listing every problem it finds. It
// stops the test only if it can't read the Go module at all.
func Check(t testing.TB, c Config) {
	t.Helper()
	problems, err := run(c)
	if err != nil {
		t.Fatalf("archtest: %v", err)
		return
	}
	if len(problems) > 0 {
		t.Errorf("archtest: %d problem(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
}

// run returns every problem in a deterministic order. Its error means the Go module
// couldn't be examined.
func run(c Config) ([]string, error) {
	dir, err := moduleDir(c.Dir)
	if err != nil {
		return nil, err
	}
	module, replaces, err := readGoMod(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, err
	}
	pkgs, problems, err := scan(dir, module)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no Go packages found in %s", dir)
	}

	if !c.AllowReplace {
		for _, r := range replaces {
			problems = append(problems, "go.mod: "+r+": remove this replace directive and use an uncommitted go.work instead")
		}
	}

	names := make([]string, 0, len(pkgs))
	for name := range pkgs {
		names = append(names, name)
	}
	slices.Sort(names)

	m := matcher{module: module, packages: names}
	for i, r := range c.Rules {
		bad := m.validate(i, r)
		problems = append(problems, bad...)
		if len(bad) > 0 {
			continue
		}
		problems = append(problems, m.violations(r, pkgs)...)
	}
	problems = append(problems, m.uncovered(c.Packages, pkgs)...)
	return problems, nil
}

// pkg is what scan learned about one package.
type pkg struct {
	imports map[string]string // import path -> first non-test file that imports it
	hasCode bool              // whether it has any non-test file
}

// matcher resolves patterns against one Go module.
type matcher struct {
	module   string
	packages []string
}

// resolve turns a local pattern into a full one, and leaves any other pattern alone.
func (m matcher) resolve(pattern string) string {
	switch {
	case pattern == ".":
		return m.module
	case strings.HasPrefix(pattern, "./"):
		return m.module + "/" + strings.TrimPrefix(pattern, "./")
	}
	return pattern
}

// local reports whether pattern names packages in this Go module.
func (m matcher) local(pattern string) bool {
	full := m.resolve(pattern)
	return full != Std && full != External && under(strings.TrimSuffix(full, "/..."), m.module)
}

// matches reports whether path is matched by pattern.
func (m matcher) matches(pattern, path string) bool {
	switch pattern {
	case Std:
		return !under(path, m.module) && !firstElementHasDot(path)
	case External:
		return !under(path, m.module) && firstElementHasDot(path)
	}
	full := m.resolve(pattern)
	if base, ok := strings.CutSuffix(full, "/..."); ok {
		return under(path, base)
	}
	return path == full
}

func (m matcher) matchesAny(patterns []string, path string) bool {
	return slices.ContainsFunc(patterns, func(p string) bool { return m.matches(p, path) })
}

// validate reports why a rule can't do its job.
func (m matcher) validate(index int, r Rule) []string {
	label := r.Name
	if label == "" {
		label = "#" + strconv.Itoa(index)
	}
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("rule %s: ", label)+fmt.Sprintf(format, args...))
	}
	if r.Name == "" {
		add("has no Name")
	}
	if r.Why == "" {
		add("has no Why")
	}
	if len(r.From) == 0 {
		add("has no From, so it checks nothing")
	}
	if len(r.Forbid) == 0 {
		add("has no Forbid, so it checks nothing")
	}
	for _, p := range append(slices.Clone(r.From), r.Except...) {
		if !m.local(p) {
			add("%q in From or Except isn't a package in %s", p, m.module)
		}
	}
	for _, group := range [][]string{r.From, r.Except, r.Forbid, r.Allow} {
		for _, p := range group {
			if m.local(p) && !slices.ContainsFunc(m.packages, func(name string) bool { return m.matches(p, name) }) {
				add("pattern %q matches no package", p)
			}
		}
	}
	return problems
}

// violations lists the imports that break a valid rule.
func (m matcher) violations(r Rule, pkgs map[string]*pkg) []string {
	var problems []string
	for _, name := range m.packages {
		if !m.matchesAny(r.From, name) || m.matchesAny(r.Except, name) {
			continue
		}
		imports := make([]string, 0, len(pkgs[name].imports))
		for imp := range pkgs[name].imports {
			imports = append(imports, imp)
		}
		slices.Sort(imports)
		for _, imp := range imports {
			if m.matchesAny(r.Forbid, imp) && !m.matchesAny(r.Allow, imp) {
				problems = append(problems, fmt.Sprintf("%s imports %s (%s)\n    rule %q: %s",
					name, imp, pkgs[name].imports[imp], r.Name, r.Why))
			}
		}
	}
	return problems
}

// uncovered reports packages that Config.Packages doesn't list, and patterns in it
// that are wrong or match nothing.
func (m matcher) uncovered(patterns []string, pkgs map[string]*pkg) []string {
	if len(patterns) == 0 {
		return nil
	}
	var problems []string
	for _, p := range patterns {
		switch {
		case !m.local(p):
			problems = append(problems, fmt.Sprintf("Packages: %q isn't a package in %s", p, m.module))
		case !slices.ContainsFunc(m.packages, func(name string) bool { return m.matches(p, name) }):
			problems = append(problems, fmt.Sprintf("Packages: pattern %q matches no package", p))
		}
	}
	for _, name := range m.packages {
		if pkgs[name].hasCode && !m.matchesAny(patterns, name) {
			problems = append(problems, fmt.Sprintf("%s isn't in Packages: decide which rules apply to it and add it", name))
		}
	}
	return problems
}

// moduleDir returns dir, or if it is empty, the nearest directory with a go.mod at or
// above the working directory.
func moduleDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod at or above the working directory")
		}
		dir = parent
	}
}

// readGoMod returns the Go module's path and its replace directives, each described
// by its line number and text.
func readGoMod(path string) (module string, replaces []string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line, _, _ := strings.Cut(sc.Text(), "//")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch {
		case fields[0] == "module" && len(fields) == 2:
			module = strings.Trim(fields[1], "\"`")
		case fields[0] == "replace" || strings.HasPrefix(fields[0], "replace("):
			replaces = append(replaces, fmt.Sprintf("line %d: %s", n, strings.TrimSpace(line)))
		}
	}
	if err := sc.Err(); err != nil {
		return "", nil, err
	}
	if module == "" {
		return "", nil, fmt.Errorf("%s has no module line", path)
	}
	return module, replaces, nil
}

// scan parses the imports of every Go file in the Go module. A file that doesn't parse
// is reported as a problem rather than stopping the scan.
func scan(dir, module string) (map[string]*pkg, []string, error) {
	pkgs := map[string]*pkg{}
	var problems []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p == dir {
				return nil
			}
			if name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				return filepath.SkipDir // a nested Go module checks itself
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		rel, err := filepath.Rel(dir, filepath.Dir(p))
		if err != nil {
			return err
		}
		path := module
		if rel != "." {
			path = module + "/" + filepath.ToSlash(rel)
		}
		if pkgs[path] == nil {
			pkgs[path] = &pkg{imports: map[string]string{}}
		}
		if strings.HasSuffix(name, "_test.go") {
			return nil
		}
		pkgs[path].hasCode = true

		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: can't read imports: %v", p, err))
			return nil
		}
		for _, spec := range f.Imports {
			imp, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
			if _, seen := pkgs[path].imports[imp]; !seen {
				pkgs[path].imports[imp] = name
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return pkgs, problems, nil
}

func under(path, base string) bool {
	return path == base || strings.HasPrefix(path, base+"/")
}

func firstElementHasDot(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return strings.Contains(first, ".")
}

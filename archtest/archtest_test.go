package archtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write creates a Go module in a temporary directory from a map of path to content.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const goMod = "module example.com/m\n\ngo 1.25\n"

func goFile(pkg string, imports ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	for _, imp := range imports {
		fmt.Fprintf(&b, "import _ %q\n", imp)
	}
	return b.String()
}

func mustRun(t *testing.T, c Config) []string {
	t.Helper()
	problems, err := run(c)
	if err != nil {
		t.Fatal(err)
	}
	return formatAll(problems, "")
}

func formatAll(ds []diagnostic, config string) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.format(config)
	}
	return out
}

func requireProblems(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d problem(s), want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.Contains(got[i], want[i]) {
			t.Errorf("problem %d = %q, want it to contain %q", i, got[i], want[i])
		}
	}
}

func TestPatterns(t *testing.T) {
	dir := write(t, map[string]string{
		"go.mod":   goMod,
		"root.go":  goFile("m", "example.com/m/a"),
		"a/a.go":   goFile("a", "os", "example.com/m/b/c", "github.com/x/y/z"),
		"b/b.go":   goFile("b"),
		"b/c/c.go": goFile("c"),
	})
	const a = "example.com/m/a"
	cases := []struct {
		name         string
		from, except []string
		forbid       []string
		allow        []string
		want         []string
	}{
		{name: "exact path", from: []string{"./a"}, forbid: []string{"./b/c"}, want: []string{a + " imports example.com/m/b/c"}},
		{name: "exact path doesn't match below it", from: []string{"./a"}, forbid: []string{"./b"}},
		{name: "subtree", from: []string{"./a"}, forbid: []string{"./b/..."}, want: []string{a + " imports example.com/m/b/c"}},
		{name: "root package", from: []string{"."}, forbid: []string{"./a"}, want: []string{"example.com/m imports " + a}},
		{name: "whole module", from: []string{"./..."}, forbid: []string{"./a"}, want: []string{"example.com/m imports " + a}},
		{name: "std", from: []string{"./a"}, forbid: []string{Std}, want: []string{a + " imports os"}},
		{name: "external", from: []string{"./a"}, forbid: []string{External}, want: []string{a + " imports github.com/x/y/z"}},
		{name: "full import path", from: []string{"./a"}, forbid: []string{"github.com/x/..."}, want: []string{a + " imports github.com/x/y/z"}},
		{name: "full path of own package", from: []string{"example.com/m/a"}, forbid: []string{"os"}, want: []string{a + " imports os"}},
		{name: "except", from: []string{"./..."}, except: []string{"./a"}, forbid: []string{"os"}},
		{name: "allow", from: []string{"./a"}, forbid: []string{External}, allow: []string{"github.com/x/..."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mustRun(t, Config{Dir: dir, Rules: []Rule{{
				Name: "r", Why: "w", From: tc.from, Except: tc.except, Forbid: tc.forbid, Allow: tc.allow,
			}}})
			requireProblems(t, got, tc.want...)
		})
	}
}

// A package path that shares a prefix with the module isn't inside it.
func TestModulePrefixIsNotMistakenForTheModule(t *testing.T) {
	dir := write(t, map[string]string{
		"go.mod": goMod,
		"a/a.go": goFile("a", "example.com/mother/x"),
	})
	got := mustRun(t, Config{Dir: dir, Rules: []Rule{{Name: "r", Why: "w", From: []string{"./a"}, Forbid: []string{"./..."}}}})
	requireProblems(t, got)
	got = mustRun(t, Config{Dir: dir, Rules: []Rule{{Name: "r", Why: "w", From: []string{"./a"}, Forbid: []string{External}}}})
	requireProblems(t, got, "imports example.com/mother/x")
}

// A required module whose path is under this one's (example.com/m/client
// beside example.com/m, as Go resolves it by the longest module path) is
// outside the module: "./..." doesn't forbid it, it can be allowed by its
// full path, and External matches it.
func TestRequiredModuleUnderTheModulePathIsOutsideIt(t *testing.T) {
	for _, mod := range []string{
		goMod + "\nrequire example.com/m/client v0.0.0\n",
		goMod + "\nrequire (\n\texample.com/other v1.0.0\n\texample.com/m/client v0.0.0 // indirect\n)\n",
	} {
		dir := write(t, map[string]string{
			"go.mod": mod,
			"a/a.go": goFile("a", "example.com/m/client", "example.com/m/b"),
			"b/b.go": goFile("b"),
		})
		got := mustRun(t, Config{Dir: dir, Rules: []Rule{{Name: "r", Why: "w", From: []string{"./a"}, Forbid: []string{"./..."}}}})
		requireProblems(t, got, "imports example.com/m/b")
		got = mustRun(t, Config{Dir: dir, Rules: []Rule{{Name: "r", Why: "w", From: []string{"./a"}, Forbid: []string{"./...", External}, Allow: []string{"./b", "example.com/m/client"}}}})
		requireProblems(t, got)
		got = mustRun(t, Config{Dir: dir, Rules: []Rule{{Name: "r", Why: "w", From: []string{"./a"}, Forbid: []string{External}}}})
		requireProblems(t, got, "imports example.com/m/client")
	}
}

func TestEveryViolationIsReportedWithItsReason(t *testing.T) {
	dir := write(t, map[string]string{
		"go.mod": goMod,
		"a/a.go": goFile("a", "os", "fmt"),
		"b/b.go": goFile("b", "os"),
	})
	got := mustRun(t, Config{Dir: dir, Rules: []Rule{{
		Name: "no std", Why: "because", From: []string{"./..."}, Forbid: []string{Std},
	}}})
	requireProblems(t, got,
		"a/a.go:3:8 [no std] repo.import_forbidden: example.com/m/a imports os\n    why: because",
		"a/a.go:4:8 [no std] repo.import_forbidden: example.com/m/a imports fmt",
		"b/b.go:3:8 [no std] repo.import_forbidden: example.com/m/b imports os")
}

func TestSkippedFilesAndDirectories(t *testing.T) {
	dir := write(t, map[string]string{
		"go.mod":              goMod,
		"a/a.go":              goFile("a"),
		"a/a_test.go":         goFile("a", "os"),
		"a/testdata/x/x.go":   goFile("x", "os"),
		"vendor/v/v.go":       goFile("v", "os"),
		".hidden/h.go":        goFile("h", "os"),
		"_skip/s.go":          goFile("s", "os"),
		"adapters/n/go.mod":   "module example.com/m/adapters/n\n",
		"adapters/n/n.go":     goFile("n", "os"),
		"onlytests/x_test.go": goFile("onlytests", "os"),
	})
	got := mustRun(t, Config{Dir: dir, Rules: []Rule{{Name: "r", Why: "w", From: []string{"./..."}, Forbid: []string{"os"}}}})
	requireProblems(t, got)
}

// An import behind a build constraint still counts, whatever platform runs the check.
func TestBuildConstraintsDoNotHideImports(t *testing.T) {
	dir := write(t, map[string]string{
		"go.mod": goMod,
		"a/a.go": "//go:build plan9\n\n" + goFile("a", "os"),
	})
	got := mustRun(t, Config{Dir: dir, Rules: []Rule{{Name: "r", Why: "w", From: []string{"./a"}, Forbid: []string{"os"}}}})
	requireProblems(t, got, "imports os")
}

func TestRulesThatCannotWorkAreReported(t *testing.T) {
	dir := write(t, map[string]string{"go.mod": goMod, "a/a.go": goFile("a")})
	cases := []struct {
		name string
		rule Rule
		want []string
	}{
		{"no name", Rule{Why: "w", From: []string{"./a"}, Forbid: []string{"os"}}, []string{"has no Name"}},
		{"no why", Rule{Name: "r", From: []string{"./a"}, Forbid: []string{"os"}}, []string{"has no Why"}},
		{"no from", Rule{Name: "r", Why: "w", Forbid: []string{"os"}}, []string{"has no From"}},
		{"no forbid", Rule{Name: "r", Why: "w", From: []string{"./a"}}, []string{"has no Forbid"}},
		{"misspelled from", Rule{Name: "r", Why: "w", From: []string{"./typo"}, Forbid: []string{"os"}}, []string{`"./typo" matches no package`}},
		{"misspelled except", Rule{Name: "r", Why: "w", From: []string{"./a"}, Except: []string{"./typo"}, Forbid: []string{"os"}}, []string{`"./typo" matches no package`}},
		{"misspelled forbid", Rule{Name: "r", Why: "w", From: []string{"./a"}, Forbid: []string{"./typo/..."}}, []string{`"./typo/..." matches no package`}},
		{"misspelled allow", Rule{Name: "r", Why: "w", From: []string{"./a"}, Forbid: []string{"os"}, Allow: []string{"./typo"}}, []string{`"./typo" matches no package`}},
		{"from outside the module", Rule{Name: "r", Why: "w", From: []string{"a"}, Forbid: []string{"os"}}, []string{`"a" in From or Except isn't a package`}},
		{"from using std", Rule{Name: "r", Why: "w", From: []string{Std}, Forbid: []string{"os"}}, []string{`"std" in From or Except isn't a package`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireProblems(t, mustRun(t, Config{Dir: dir, Rules: []Rule{tc.rule}}), tc.want...)
		})
	}
}

func TestPackagesCoverage(t *testing.T) {
	dir := write(t, map[string]string{
		"go.mod":              goMod,
		"a/a.go":              goFile("a"),
		"b/b.go":              goFile("b"),
		"onlytests/x_test.go": goFile("onlytests"),
	})
	requireProblems(t, mustRun(t, Config{Dir: dir}))
	requireProblems(t, mustRun(t, Config{Dir: dir, Packages: []string{"./a", "./b"}}))
	requireProblems(t, mustRun(t, Config{Dir: dir, Packages: []string{"./a"}}), "example.com/m/b isn't in Packages")
	requireProblems(t, mustRun(t, Config{Dir: dir, Packages: []string{"./...", "./typo"}}), `"./typo" matches no package`)
	requireProblems(t, mustRun(t, Config{Dir: dir, Packages: []string{"./...", "os"}}), `"os" isn't a package`)
}

func TestReplaceDirectives(t *testing.T) {
	cases := map[string]struct {
		goMod string
		want  []string
	}{
		"none":           {goMod, nil},
		"single":         {goMod + "replace a.com/b => ../b\n", []string{"go.mod:4 repo.replace_forbidden: replace a.com/b => ../b"}},
		"block":          {goMod + "replace (\n\ta.com/b => ../b\n)\n", []string{"go.mod:4 repo.replace_forbidden: replace ("}},
		"no space":       {goMod + "replace(\n\ta.com/b => ../b\n)\n", []string{"go.mod:4 repo.replace_forbidden: replace("}},
		"in a comment":   {goMod + "// replace a.com/b => ../b\n", nil},
		"a similar word": {goMod + "replacement x\n", nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := write(t, map[string]string{"go.mod": tc.goMod, "a/a.go": goFile("a")})
			requireProblems(t, mustRun(t, Config{Dir: dir}), tc.want...)
			requireProblems(t, mustRun(t, Config{Dir: dir, AllowReplace: true}))
		})
	}
}

func TestUnparseableFileIsReportedWithTheRest(t *testing.T) {
	dir := write(t, map[string]string{
		"go.mod": goMod,
		"a/a.go": "not go",
		"b/b.go": goFile("b", "os"),
	})
	got := mustRun(t, Config{Dir: dir, Rules: []Rule{{Name: "r", Why: "w", From: []string{"./b"}, Forbid: []string{"os"}}}})
	requireProblems(t, got, "a/a.go repo.parse_failed: can't read imports: a/a.go:1:1", "imports os")
}

func TestModuleThatCannotBeExaminedIsAnError(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"no go.mod":      {"a/a.go": goFile("a")},
		"no module line": {"go.mod": "go 1.25\n", "a/a.go": goFile("a")},
		"no packages":    {"go.mod": goMod},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := run(Config{Dir: write(t, files)}); err == nil {
				t.Fatal("got no error")
			}
		})
	}
}

// fakeT records how Check fails, without failing the real test.
type fakeT struct {
	testing.TB
	errors, fatals []string
}

func (f *fakeT) Helper() {}
func (f *fakeT) Errorf(format string, args ...any) {
	f.errors = append(f.errors, fmt.Sprintf(format, args...))
}
func (f *fakeT) Fatalf(format string, args ...any) {
	f.fatals = append(f.fatals, fmt.Sprintf(format, args...))
}

func TestCheckFailsTheTest(t *testing.T) {
	dir := write(t, map[string]string{"go.mod": goMod, "a/a.go": goFile("a", "os")})
	rule := Rule{Name: "r", Why: "w", From: []string{"./a"}, Forbid: []string{"os"}}

	var clean fakeT
	Check(&clean, Config{Dir: dir})
	if len(clean.errors)+len(clean.fatals) != 0 {
		t.Errorf("clean module failed: %+v", clean)
	}

	var broken fakeT
	Check(&broken, Config{Dir: dir, Rules: []Rule{rule}})
	if len(broken.errors) != 1 || !strings.Contains(broken.errors[0], "1 problem(s)") {
		t.Errorf("violation: errors = %q", broken.errors)
	}

	var missing fakeT
	Check(&missing, Config{Dir: filepath.Join(dir, "absent")})
	if len(missing.fatals) != 1 {
		t.Errorf("unreadable module: fatals = %q", missing.fatals)
	}
}

// With no Dir, Check finds the go.mod above the test's working directory.
func TestDefaultDirFindsThisModule(t *testing.T) {
	dir, err := moduleDir("")
	if err != nil {
		t.Fatal(err)
	}
	module, _, _, err := readGoMod(filepath.Join(dir, "go.mod"))
	if err != nil || module != "github.com/craggy-io/foundation-kit" {
		t.Fatalf("module = %q, %v", module, err)
	}
}

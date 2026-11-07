package archtest

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// evalRepo writes files into a new repository and evaluates its archtest.yaml.
func evalRepo(t *testing.T, files map[string]string) []string {
	t.Helper()
	return evalDir(t, write(t, files), defaultLimits)
}

func evalDir(t *testing.T, dir string, lim limits) []string {
	t.Helper()
	ds, _, name := evaluate(context.Background(), filepath.Join(dir, "archtest.yaml"), lim)
	return formatAll(ds, name)
}

// requireSet checks that got has exactly one problem containing each want, in any
// order.
func requireSet(t *testing.T, got []string, want ...string) {
	t.Helper()
	used := make([]bool, len(got))
	for _, w := range want {
		i := slices.IndexFunc(got, func(g string) bool { return strings.Contains(g, w) })
		for i >= 0 && used[i] {
			j := slices.IndexFunc(got[i+1:], func(g string) bool { return strings.Contains(g, w) })
			if j < 0 {
				i = -1
				break
			}
			i += j + 1
		}
		if i < 0 {
			t.Errorf("no problem contains %q", w)
			continue
		}
		used[i] = true
	}
	for i, g := range got {
		if !used[i] {
			t.Errorf("unexpected problem:\n%s", g)
		}
	}
}

// with returns a copy of base with changes applied; an empty value deletes a file.
func with(base map[string]string, changes map[string]string) map[string]string {
	out := maps.Clone(base)
	for k, v := range changes {
		if v == "" {
			delete(out, k)
		} else {
			out[k] = v
		}
	}
	return out
}

// --- Example: code in src, authored content in res, no embedding ---

const layoutConfig = `version: 1
rules:
  - id: declared-top-level-layout
    why: Keep code, authored content, and documentation in their declared locations.
    select: {kind: directory, include: ['.']}
    tree:
      require:
        directories: [src, res, docs]
      allow:
        files: [archtest.yaml, README.md, AGENTS.md, LICENSE, .gitignore]
        directories: [src, res, docs, .github, var, build]
  - id: source-files-only
    why: Authored content belongs under root res.
    select:
      kind: directory
      include: [src, 'src/**']
    tree:
      allow:
        files: ['*.go', go.mod, go.sum, README.md, AGENTS.md]
        directories: ['*']
      forbid: [res, assets, data, runtime, generated, build, '*.db', '*.sqlite', '*.log']
  - id: no-go-embedding
    why: Load authored bundles from root res through the bundle pipeline.
    select:
      kind: file
      include: ['src/**/*.go']
    go_source:
      forbid_imports: [embed]
      forbid_directives: [go:embed]
`

var layoutRepo = map[string]string{
	"archtest.yaml":        layoutConfig,
	"README.md":            "# App\n",
	"src/go.mod":           "module example.org/app\n\ngo 1.25\n",
	"src/main.go":          "package main\n\n// The string \"//go:embed x\" and /* //go:embed y */ aren't directives.\nvar s = \"//go:embed z\"\n\n// go:embed spaced isn't one either.\nfunc main() {}\n",
	"src/features/x/x.go":  "package x\n",
	"res/review/README.md": "# Review\n",
	"docs/README.md":       "# Docs\n",
}

func TestLayoutAcceptedTree(t *testing.T) {
	requireSet(t, evalRepo(t, layoutRepo))
}

func TestLayoutViolations(t *testing.T) {
	cases := map[string]struct {
		changes map[string]string
		want    []string
	}{
		"content in src": {map[string]string{"src/features/x/data.json": "{}"},
			[]string{`src/features/x/data.json [source-files-only] repo.entry_unexpected`}},
		"forbidden dir in src": {map[string]string{"src/res/a.go": "package res\n"},
			[]string{`src/res [source-files-only] repo.entry_forbidden`}},
		"database in src": {map[string]string{"src/app.db": "x"},
			[]string{`src/app.db [source-files-only] repo.entry_forbidden`}},
		"new top-level dir": {map[string]string{"assets/x.png": "x"},
			[]string{`assets [declared-top-level-layout] repo.entry_unexpected`}},
		"missing docs": {map[string]string{"docs/README.md": ""},
			[]string{`docs [declared-top-level-layout] repo.entry_missing: required directory "docs" is missing`}},
		"embed imports": {map[string]string{
			"src/a.go": "package main\n\nimport _ \"embed\"\n",
			"src/b.go": "package main\n\nimport e \"embed\"\n\nvar _ e.FS\n",
			"src/c.go": "package main\n\nimport . \"embed\"\n\nvar _ FS\n",
			"src/d.go": "package main\n\nimport \"embed\"\n\nvar _ embed.FS\n",
		}, []string{
			`src/a.go:3:8 [no-go-embedding] repo.import_forbidden: imports "embed" as _`,
			`src/b.go:3:8 [no-go-embedding] repo.import_forbidden: imports "embed" as e`,
			`src/c.go:3:8 [no-go-embedding] repo.import_forbidden: imports "embed" as .`,
			`src/d.go:3:8 [no-go-embedding] repo.import_forbidden: imports "embed"`,
		}},
		"directives, indented and repeated": {map[string]string{
			"src/e.go": "package main\n\nfunc f() {\n\t//go:embed a.txt\n\t//go:embed\n}\n",
		}, []string{
			`src/e.go:4:2 [no-go-embedding] repo.directive_forbidden: has a //go:embed directive`,
			`src/e.go:5:2 [no-go-embedding] repo.directive_forbidden`,
		}},
		"behind a build tag and in a test": {map[string]string{
			"src/f.go":      "//go:build ignore\n\npackage main\n\nimport _ \"embed\"\n",
			"src/f_test.go": "package main\n\n//go:embed x\nvar x string\n",
		}, []string{
			`src/f.go:5:8 [no-go-embedding] repo.import_forbidden`,
			`src/f_test.go:3:1 [no-go-embedding] repo.directive_forbidden`,
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			requireSet(t, evalRepo(t, with(layoutRepo, tc.changes)), tc.want...)
		})
	}
}

func TestDiagnosticsCarryWhyAndRulePosition(t *testing.T) {
	got := evalRepo(t, with(layoutRepo, map[string]string{"src/app.db": "x"}))
	requireSet(t, got, "src/app.db [source-files-only] repo.entry_forbidden: file \"app.db\" is forbidden here\n"+
		"    why: Authored content belongs under root res.\n"+
		"    rule defined at archtest.yaml:12:5")
}

func TestExceptions(t *testing.T) {
	config := strings.Replace(layoutConfig, "  - id: no-go-embedding\n", `  - id: no-go-embedding
    exceptions:
      - {path: src/example_test.go, why: This isolated test verifies embedding compatibility.}
`, 1)
	repo := with(layoutRepo, map[string]string{
		"archtest.yaml":       config,
		"src/example_test.go": "package main\n\nimport _ \"embed\"\n\n//go:embed x\nvar x string\n",
	})
	requireSet(t, evalRepo(t, repo))

	// An exception that suppresses nothing is stale.
	repo["src/example_test.go"] = "package main\n"
	requireSet(t, evalRepo(t, repo),
		`archtest.yaml:24:9 [no-go-embedding] repo.exception_stale: the exception for "src/example_test.go" suppresses nothing`)

	// An exception covers only its exact path.
	repo["src/example_test.go"] = "package main\n"
	repo["src/other_test.go"] = "package main\n\nimport _ \"embed\"\n"
	requireSet(t, evalRepo(t, repo), "repo.exception_stale", "src/other_test.go:3:8 [no-go-embedding] repo.import_forbidden")
}

// --- Example: grouping folders and independent leaf bundles ---

const groupsConfig = `version: 1
exclude:
  - path: build/**
    why: Build output is not authored content.
rules:
  - id: content-groups
    why: Keep distributions in named leaf bundles, not loose files or a group-wide bundle.
    select:
      kind: directory
      include: [res]
    tree:
      require:
        files: [README.md, AGENTS.md]
        directories: [workflows, models]
      allow:
        files: [README.md, AGENTS.md]
        directories: [workflows, models]
      forbid: [bundle.yaml]
  - id: content-group-children
    why: Each child directory is one independently loadable distribution.
    select:
      kind: directory
      include: [res/workflows, res/models]
    tree:
      allow:
        files: [README.md, AGENTS.md]
        directories: ['*']
      forbid: [bundle.yaml]
      child_directories:
        min: 1
        require_files: [bundle.yaml, README.md]
        forbid_descendants: ['*/**/bundle.yaml']
  - id: no-catch-all-bundle-names
    why: Name a distribution for its purpose; do not collect unrelated definitions.
    select:
      kind: directory
      include: [res/workflows, res/models]
    tree:
      forbid: [all, misc, common]
`

var groupsRepo = map[string]string{
	"archtest.yaml":                         groupsConfig,
	"res/README.md":                         "# Content\n",
	"res/AGENTS.md":                         "# Agents\n",
	"res/workflows/README.md":               "# Workflows\n",
	"res/workflows/review/bundle.yaml":      "apiVersion: craggy.io/bundle/v1\n",
	"res/workflows/review/README.md":        "# Review\n",
	"res/workflows/review/review.star":      "x = 1\n",
	"res/workflows/review/assets/prompt.md": "prompt\n",
	"res/models/issue-triage/bundle.yaml":   "apiVersion: craggy.io/bundle/v1\n",
	"res/models/issue-triage/README.md":     "# Triage\n",
	"res/models/issue-triage/model.json":    "{}",
	"build/anything/goes/here/bundle.yaml":  "x",
	"build/loose.bin":                       "x",
}

func TestGroupsAcceptedTree(t *testing.T) {
	requireSet(t, evalRepo(t, groupsRepo))
}

func TestGroupsViolations(t *testing.T) {
	cases := map[string]struct {
		changes map[string]string
		want    []string
	}{
		"loose file in a group": {map[string]string{"res/workflows/review.star": "x"},
			[]string{`res/workflows/review.star [content-group-children] repo.entry_unexpected`}},
		"group-wide bundle": {map[string]string{"res/models/bundle.yaml": "x"},
			[]string{`res/models/bundle.yaml [content-group-children] repo.entry_forbidden`}},
		"child without a manifest": {map[string]string{"res/models/issue-triage/bundle.yaml": ""},
			[]string{`res/models/issue-triage/bundle.yaml [content-group-children] repo.entry_missing: required file "bundle.yaml" is missing`}},
		"child without a README": {map[string]string{"res/models/issue-triage/README.md": ""},
			[]string{`res/models/issue-triage/README.md [content-group-children] repo.entry_missing`}},
		"nested manifest": {map[string]string{"res/workflows/review/assets/inner/bundle.yaml": "x"},
			[]string{`res/workflows/review/assets/inner/bundle.yaml [content-group-children] repo.entry_forbidden`}},
		"catch-all name": {map[string]string{"res/models/misc/bundle.yaml": "x", "res/models/misc/README.md": "x"},
			[]string{`res/models/misc [no-catch-all-bundle-names] repo.entry_forbidden`}},
		"empty group": {map[string]string{"res/models/issue-triage/bundle.yaml": "", "res/models/issue-triage/README.md": "",
			"res/models/issue-triage/model.json": "", "res/models/README.md": "x"},
			[]string{`res/models [content-group-children] repo.child_count: has 0 child directories, want at least 1`}},
		"missing group": {map[string]string{"res/models/issue-triage/bundle.yaml": "", "res/models/issue-triage/README.md": "",
			"res/models/issue-triage/model.json": ""},
			[]string{
				`res/models [content-groups] repo.entry_missing`,
				// The selector's literal include now matches nothing.
				`archtest.yaml:22:7 [content-group-children] repo.selector_empty: select.include "res/models" matches no directory`,
				`archtest.yaml:36:7 [no-catch-all-bundle-names] repo.selector_empty`,
			}},
		"missing root docs": {map[string]string{"res/AGENTS.md": ""},
			[]string{`res/AGENTS.md [content-groups] repo.entry_missing`}},
		"unlisted group": {map[string]string{"res/agents/x/bundle.yaml": "x"},
			[]string{`res/agents [content-groups] repo.entry_unexpected`}},
		// A renamed catch-all is a documented semantic limit: nothing catches it.
		"renamed catch-all passes": {map[string]string{"res/models/useful-things/bundle.yaml": "x", "res/models/useful-things/README.md": "x"}, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			requireSet(t, evalRepo(t, with(groupsRepo, tc.changes)), tc.want...)
		})
	}
}

// --- Example: README structure and metadata ---

const readmeConfig = `version: 1
rules:
  - id: bundle-readme
    why: Explain the distribution's purpose, use, and current implementation status.
    select:
      kind: file
      include: [res/workflows/*/README.md]
    markdown:
      frontmatter:
        required: true
        schema:
          type: object
          required: [title, owner, status]
          additionalProperties: false
          properties:
            title: {type: string, minLength: 1}
            owner: {type: string, minLength: 1}
            status: {enum: [draft, ready, experimental]}
      headings:
        - {text: Purpose, level: 2, count: 1, body: nonempty}
        - {text: Usage, level: 2, count: 1, body: nonempty}
        - {text: Implementation status, level: 2, count: 1, body: nonempty}
      order: [Purpose, Usage, Implementation status]
`

const goodReadme = `---
title: Review workflow
owner: developer-tools
status: ready
---
# Review

## Purpose

Reviews changes.

## Usage

` + "```sh\nrun review\n```" + `

## Implementation ` + "`status`" + `

Working.
`

func readmeRepo(readme string) map[string]string {
	return map[string]string{"archtest.yaml": readmeConfig, "res/workflows/review/README.md": readme}
}

func TestReadmeAccepted(t *testing.T) {
	requireSet(t, evalRepo(t, readmeRepo(goodReadme)))
	crlf := "\xEF\xBB\xBF" + strings.ReplaceAll(goodReadme, "\n", "\r\n")
	requireSet(t, evalRepo(t, readmeRepo(crlf)))
}

func TestReadmeViolations(t *testing.T) {
	const file = "res/workflows/review/README.md"
	cases := map[string]struct {
		readme string
		want   []string
	}{
		"missing heading": {strings.Replace(goodReadme, "## Usage", "## How to use", 1),
			[]string{file + ` [bundle-readme] repo.heading_missing: required H2 "Usage" is absent`}},
		"heading inside a fence": {strings.Replace(goodReadme, "## Usage\n", "```\n## Usage\n```\n", 1),
			[]string{file + ` [bundle-readme] repo.heading_missing: required H2 "Usage" is absent`}},
		"wrong level": {strings.Replace(goodReadme, "## Usage", "### Usage", 1),
			[]string{`repo.heading_missing: required H2 "Usage"`}},
		"duplicate heading": {goodReadme + "\n## Usage\n\nAgain.\n",
			[]string{file + `:22 [bundle-readme] repo.heading_count: H2 "Usage" appears 2 times, want 1`}},
		"empty section": {strings.Replace(goodReadme, "Reviews changes.\n", "<!-- todo -->\n", 1),
			[]string{file + `:8 [bundle-readme] repo.section_empty: the H2 "Purpose" section is empty`}},
		"out of order": {strings.Replace(strings.Replace(goodReadme, "## Purpose", "## Tmp", 1), "## Usage", "## Purpose", 1),
			[]string{
				`repo.heading_missing: required H2 "Usage"`,
			}},
		"missing frontmatter": {goodReadme[strings.Index(goodReadme, "# Review"):],
			[]string{file + ` [bundle-readme] repo.frontmatter_missing`}},
		"unterminated frontmatter": {strings.Replace(goodReadme, "status: ready\n---\n", "status: ready\n", 1),
			[]string{file + `:1:1 [bundle-readme] repo.frontmatter_invalid: the frontmatter that starts on line 1 has no closing --- line`}},
		"bad status": {strings.Replace(goodReadme, "status: ready", "status: done", 1),
			[]string{file + `:4:9 [bundle-readme] repo.schema_invalid /status: value must be one of`}},
		"unknown and missing fields": {strings.Replace(goodReadme, "owner: developer-tools", "Owner: developer-tools", 1),
			[]string{
				file + `:2:1 [bundle-readme] repo.schema_invalid: additional properties 'Owner' not allowed`,
				file + `:2:1 [bundle-readme] repo.schema_invalid: missing property 'owner'`,
			}},
		"null versus absent": {strings.Replace(goodReadme, "owner: developer-tools", "owner:", 1),
			[]string{file + `:3:7 [bundle-readme] repo.schema_invalid /owner: got null, want string`}},
		"duplicate frontmatter key": {strings.Replace(goodReadme, "status: ready", "status: ready\nstatus: draft", 1),
			[]string{file + `:5:1 [bundle-readme] repo.frontmatter_invalid: frontmatter duplicate key "status"`}},
		"not UTF-8": {"\xff\xfe", []string{file + ` [bundle-readme] repo.parse_failed: isn't valid UTF-8`}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			requireSet(t, evalRepo(t, readmeRepo(tc.readme)), tc.want...)
		})
	}
}

func TestReadmeOrder(t *testing.T) {
	readme := strings.Replace(goodReadme, "## Purpose\n\nReviews changes.\n\n", "", 1) + "\n## Purpose\n\nLate.\n"
	requireSet(t, evalRepo(t, readmeRepo(readme)),
		`:8 [bundle-readme] repo.heading_order: "Usage" must come after "Purpose" (line 18)`)
}

// --- Example: Go import boundaries ---

const importsConfig = `version: 1
rules:
  - id: contracts-stay-small
    why: Contracts must not depend on application implementations or third-party libraries.
    imports:
      module: src
      packages: ['./contracts/...', './features/...', './adapters/...']
      from: ['./contracts/...']
      forbid: ['./features/...', './adapters/...', external]
      allow: []
`

var importsRepo = map[string]string{
	"archtest.yaml":            importsConfig,
	"src/go.mod":               "module example.org/app\n\ngo 1.25\n",
	"src/contracts/model.go":   "package contracts\n\nimport _ \"fmt\"\n",
	"src/features/review/r.go": "package review\n\nimport _ \"example.org/app/contracts\"\n",
	"src/adapters/db/db.go":    "package db\n\nimport _ \"github.com/x/y\"\n",
	"src/contracts/x_test.go":  "package contracts\n\nimport _ \"example.org/app/features/review\"\n",
	"src/repository_test.go":   "package repository_test\n",
}

func TestImportsAccepted(t *testing.T) {
	requireSet(t, evalRepo(t, importsRepo))
}

func TestImportsViolations(t *testing.T) {
	cases := map[string]struct {
		changes map[string]string
		want    []string
	}{
		"forbidden import": {map[string]string{"src/contracts/model.go": "package contracts\n\nimport _ \"example.org/app/features/review\"\n"},
			[]string{"src/contracts/model.go:3:8 [contracts-stay-small] repo.import_forbidden: example.org/app/contracts imports example.org/app/features/review\n" +
				"    why: Contracts must not depend on application implementations or third-party libraries.\n" +
				"    rule defined at archtest.yaml:3:5"}},
		"external import": {map[string]string{"src/contracts/ext.go": "package contracts\n\nimport _ \"github.com/x/y\"\n"},
			[]string{"src/contracts/ext.go:3:8 [contracts-stay-small] repo.import_forbidden"}},
		"uncovered package": {map[string]string{"src/tools/t.go": "package tools\n"},
			[]string{"src/tools [contracts-stay-small] repo.package_uncovered"}},
		"misspelled pattern": {map[string]string{"archtest.yaml": strings.Replace(importsConfig, "from: ['./contracts/...']", "from: ['./contract/...']", 1)},
			[]string{`archtest.yaml:3:5 [contracts-stay-small] repo.pattern_unmatched: pattern "./contract/..." matches no package`}},
		"replace directive": {map[string]string{"src/go.mod": "module example.org/app\n\ngo 1.25\n\nreplace a.com/b => ../b\n"},
			[]string{"src/go.mod:5 [contracts-stay-small] repo.replace_forbidden"}},
		"missing module": {map[string]string{"archtest.yaml": strings.Replace(importsConfig, "module: src", "module: Src", 1)},
			[]string{`archtest.yaml:6:15 [contracts-stay-small] repo.config_invalid: imports.module "Src" has no go.mod`}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			requireSet(t, evalRepo(t, with(importsRepo, tc.changes)), tc.want...)
		})
	}
}

func TestImportRulesForOneModuleShareAScan(t *testing.T) {
	config := importsConfig + `  - id: features-skip-adapters
    why: Features receive adapters as dependencies.
    imports:
      module: src
      from: ['./features/...']
      forbid: ['./adapters/...']
`
	repo := with(importsRepo, map[string]string{
		"archtest.yaml":            config,
		"src/features/review/r.go": "package review\n\nimport _ \"example.org/app/adapters/db\"\n",
	})
	requireSet(t, evalRepo(t, repo), "src/features/review/r.go:3:8 [features-skip-adapters] repo.import_forbidden")

	// Rules for one module must agree on its inventory.
	repo["archtest.yaml"] = config + "      packages: ['./...']\n"
	requireSet(t, evalRepo(t, repo), "repo.config_invalid: imports.packages for module \"src\" differs")
}

// --- Example: structured JSON or YAML ---

const profilesConfig = `version: 1
rules:
  - id: profile-shape
    why: Every profile must declare its identity and a positive attempt limit.
    select:
      kind: file
      include: [config/profiles/*.yaml, config/profiles/*.json]
    structured:
      format: auto
      schema:
        type: object
        required: [id, limits]
        additionalProperties: false
        properties:
          id: {type: string, minLength: 1}
          limits:
            type: object
            required: [attempts]
            additionalProperties: false
            properties:
              attempts: {type: integer, minimum: 1}
`

var profilesRepo = map[string]string{
	"archtest.yaml":                profilesConfig,
	"config/profiles/default.yaml": "id: default\nlimits:\n  attempts: 3\n",
	"config/profiles/strict.json":  "{\n  \"id\": \"strict\",\n  \"limits\": {\"attempts\": 12345678901234567890}\n}\n",
}

func TestStructuredAccepted(t *testing.T) {
	requireSet(t, evalRepo(t, profilesRepo))
}

func TestStructuredViolations(t *testing.T) {
	const y, j = "config/profiles/default.yaml", "config/profiles/strict.json"
	cases := map[string]struct {
		changes map[string]string
		want    []string
	}{
		"yaml schema failure": {map[string]string{y: "id: default\nlimits:\n  attempts: 0\n"},
			[]string{y + `:3:13 [profile-shape] repo.schema_invalid /limits/attempts: minimum: got 0, want 1`}},
		"json schema failure": {map[string]string{j: "{\n  \"id\": \"strict\",\n  \"limits\": {\"attempts\": 1.5}\n}\n"},
			[]string{j + `:3:26 [profile-shape] repo.schema_invalid /limits/attempts: got number, want integer`}},
		"json duplicate key": {map[string]string{j: "{\"id\": \"a\", \"id\": \"b\", \"limits\": {\"attempts\": 1}}"},
			[]string{j + `:1:13 [profile-shape] repo.parse_failed: duplicate key "id"`}},
		"json trailing value": {map[string]string{j: "{\"id\": \"a\", \"limits\": {\"attempts\": 1}} {}"},
			[]string{j + `:1:41 [profile-shape] repo.parse_failed: only one JSON value is allowed`}},
		"json syntax error": {map[string]string{j: "{\"id\": \"a\",\n  \"limits\": }"},
			[]string{j + `:2:13 [profile-shape] repo.parse_failed`}},
		"empty file": {map[string]string{y: "\n"},
			[]string{y + ` [profile-shape] repo.parse_failed: is empty`}},
		"yaml second document": {map[string]string{y: "id: a\nlimits: {attempts: 1}\n---\nid: b\n"},
			[]string{y + `:3:1 [profile-shape] repo.parse_failed: only one YAML document is allowed`}},
		"yaml anchor": {map[string]string{y: "id: &x a\nlimits: {attempts: 1}\n"},
			[]string{y + `:1:5 [profile-shape] repo.parse_failed: anchors are not allowed`}},
		"one extension missing": {map[string]string{j: ""},
			[]string{`archtest.yaml:6:7 [profile-shape] repo.selector_empty: select.include "config/profiles/*.json" matches no file`}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			requireSet(t, evalRepo(t, with(profilesRepo, tc.changes)), tc.want...)
		})
	}
}

func TestSchemaFile(t *testing.T) {
	config := `version: 1
rules:
  - id: profile-shape
    why: w
    select: {kind: file, include: [config/profiles/*.yaml]}
    structured: {schema_file: schemas/profile.json}
`
	repo := map[string]string{
		"archtest.yaml":                config,
		"schemas/profile.json":         `{"type": "object", "required": ["id"], "$defs": {"id": {"type": "string"}}, "properties": {"id": {"$ref": "#/$defs/id"}}}`,
		"config/profiles/default.yaml": "id: 3\n",
	}
	requireSet(t, evalRepo(t, repo), "config/profiles/default.yaml:1:5 [profile-shape] repo.schema_invalid /id: got number, want string")

	repo["schemas/profile.json"] = `{"$ref": "https://example.com/remote.json"}`
	requireSet(t, evalRepo(t, repo), "archtest.yaml:6:31 [profile-shape] repo.config_invalid: schema_file \"schemas/profile.json\"")

	repo["archtest.yaml"] = strings.Replace(config, "schemas/profile.json", "schemas/Profile.json", 1)
	requireSet(t, evalRepo(t, repo), "repo.config_invalid: schema_file \"schemas/Profile.json\": not found (names are case-sensitive)")
}

// --- Configuration validation ---

func TestInvalidConfiguration(t *testing.T) {
	const rule = "  - id: r\n    why: w\n    select: {kind: directory, include: ['.']}\n    tree: {forbid: [x]}\n"
	cases := map[string]struct {
		config string
		want   []string
	}{
		"empty":                   {"", []string{"archtest.yaml:1:1 repo.config_invalid: the file is empty"}},
		"unknown version":         {"version: 2\nrules:\n" + rule, []string{"archtest.yaml:1:10 repo.config_invalid: version 2 isn't supported"}},
		"missing version":         {"rules:\n" + rule, []string{"version is required"}},
		"unknown top-level field": {"version: 1\nrule: []\n", []string{`unknown field "rule"`, "rules is required"}},
		"unknown nested field": {"version: 1\nrules:\n" + strings.Replace(rule, "forbid", "forbids", 1),
			[]string{`archtest.yaml:6:12 [r] repo.config_invalid: tree has unknown field "forbids"`, "tree checks nothing"}},
		"empty rules":    {"version: 1\nrules: []\n", []string{"rules is empty"}},
		"duplicate id":   {"version: 1\nrules:\n" + rule + rule, []string{`rule id "r" is already used on line 3`}},
		"duplicate key":  {"version: 1\nversion: 1\nrules:\n" + rule, []string{`duplicate key "version"`}},
		"alias":          {"version: 1\nrules:\n  - &a {id: r, why: w, select: {kind: directory, include: ['.']}, tree: {forbid: [x]}}\n  - *a\n", []string{"anchors are not allowed", "aliases are not allowed"}},
		"no check":       {"version: 1\nrules:\n  - {id: r, why: w}\n", []string{"has no check"}},
		"two checks":     {"version: 1\nrules:\n" + rule + "    go_source: {forbid_imports: [embed]}\n", []string{"has tree and go_source"}},
		"missing why":    {"version: 1\nrules:\n  - {id: r, select: {kind: directory, include: ['.']}, tree: {forbid: [x]}}\n", []string{"is missing why"}},
		"empty why":      {"version: 1\nrules:\n" + strings.Replace(rule, "why: w", "why: ''", 1), []string{"why is empty"}},
		"wrong kind":     {"version: 1\nrules:\n" + strings.Replace(rule, "kind: directory", "kind: file", 1), []string{"a tree rule selects kind directory, not file"}},
		"missing select": {"version: 1\nrules:\n  - {id: r, why: w, tree: {forbid: [x]}}\n", []string{"needs a select"}},
		"select on imports": {"version: 1\nrules:\n  - {id: r, why: w, select: {kind: file, include: [a]}, imports: {module: ., from: [.], forbid: [os]}}\n",
			[]string{"an imports rule has no select"}},
		"backslash":                {"version: 1\nrules:\n" + strings.Replace(rule, "include: ['.']", `include: ['a\b']`, 1), []string{"uses a backslash"}},
		"dot dot":                  {"version: 1\nrules:\n" + strings.Replace(rule, "include: ['.']", `include: [../x]`, 1), []string{`has a ".." component`}},
		"leading dot slash":        {"version: 1\nrules:\n" + strings.Replace(rule, "include: ['.']", `include: [./x]`, 1), []string{"starts with ./"}},
		"absolute":                 {"version: 1\nrules:\n" + strings.Replace(rule, "include: ['.']", `include: ['C:/x']`, 1), []string{"is absolute"}},
		"braces":                   {"version: 1\nrules:\n" + strings.Replace(rule, "include: ['.']", `include: ['{a,b}']`, 1), []string{"brackets or braces"}},
		"bad double star":          {"version: 1\nrules:\n" + strings.Replace(rule, "include: ['.']", `include: ['a**']`, 1), []string{"** must be a whole component"}},
		"slash in child list":      {"version: 1\nrules:\n" + strings.Replace(rule, "forbid: [x]", "forbid: [a/b]", 1), []string{"names immediate children only"}},
		"on_empty without reason":  {"version: 1\nrules:\n" + strings.Replace(rule, "include: ['.']", "include: ['.'], on_empty: allow", 1), []string{"needs an empty_reason"}},
		"require and forbid":       {"version: 1\nrules:\n" + strings.Replace(rule, "tree: {forbid: [x]}", "tree: {forbid: [x], require: {files: [x]}}", 1), []string{`both requires and forbids "x"`}},
		"required but not allowed": {"version: 1\nrules:\n" + strings.Replace(rule, "tree: {forbid: [x]}", "tree: {require: {files: [a]}, allow: {files: [b]}}", 1), []string{`requires file "a" but its allow list doesn't permit it`}},
		"wrong type":               {"version: 1\nrules:\n" + strings.Replace(rule, "forbid: [x]", "forbid: x", 1), []string{"tree.forbid must be a sequence, not a string", "tree checks nothing"}},
		"bad schema":               {"version: 1\nrules:\n  - {id: r, why: w, select: {kind: file, include: [a]}, structured: {schema: {type: nope}}}\n", []string{"schema: jsonschema: compiling the document"}},
		"order without heading": {"version: 1\nrules:\n  - {id: r, why: w, select: {kind: file, include: [a]}, markdown: {headings: [{text: A, level: 2}], order: [A, B]}}\n",
			[]string{`markdown.order: "B" must name exactly one heading with count 1`}},
		"exceptions on imports": {"version: 1\nrules:\n  - {id: r, why: w, exceptions: [{path: a, why: b}], imports: {module: ., from: [.], forbid: [os]}}\n",
			[]string{"an imports rule has no exceptions"}},
		"wildcard exception": {"version: 1\nrules:\n" + rule + "    exceptions: [{path: 'a/*', why: b}]\n", []string{"must be a literal path"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := evalRepo(t, map[string]string{"archtest.yaml": tc.config, "x/a.txt": "x"})
			requireSet(t, got, tc.want...)
		})
	}
}

func TestMissingConfigurationFails(t *testing.T) {
	var ft fakeT
	CheckFile(context.Background(), &ft, filepath.Join(t.TempDir(), "archtest.yaml"))
	if len(ft.errors) != 1 || !strings.Contains(ft.errors[0], "can't read the configuration") {
		t.Errorf("errors = %q", ft.errors)
	}
}

func TestCheckFileReportsAndLogs(t *testing.T) {
	config := strings.Replace(groupsConfig, "  - id: content-groups\n", "  - id: content-groups\n    exceptions: [{path: res/stray.txt, why: Kept for a migration.}]\n", 1)
	dir := write(t, with(groupsRepo, map[string]string{"archtest.yaml": config, "res/stray.txt": "x"}))
	var ft logT
	CheckFile(context.Background(), &ft, filepath.Join(dir, "archtest.yaml"))
	if len(ft.errors)+len(ft.fatals) != 0 {
		t.Fatalf("clean repository failed: %q", ft.errors)
	}
	logs := strings.Join(ft.logs, "\n")
	for _, want := range []string{"excluded build", "[content-groups] exception for res/stray.txt suppressed repo.entry_unexpected: Kept for a migration."} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs don't mention %q:\n%s", want, logs)
		}
	}

	var broken logT
	os.WriteFile(filepath.Join(dir, "res", "loose.txt"), []byte("x"), 0o644)
	CheckFile(context.Background(), &broken, filepath.Join(dir, "archtest.yaml"))
	if len(broken.errors) != 1 || !strings.Contains(broken.errors[0], "archtest: archtest.yaml: 1 problem(s):\n  res/loose.txt [content-groups]") {
		t.Errorf("errors = %q", broken.errors)
	}
}

type logT struct {
	fakeT
	logs []string
}

func (l *logT) Logf(format string, args ...any) {
	l.logs = append(l.logs, strings.TrimSpace(fmt.Sprintf(format, args...)))
}

// --- Selectors, exclusions and portability ---

func TestSelectors(t *testing.T) {
	base := `version: 1
rules:
  - id: r
    why: w
    select: SELECT
    go_source: {forbid_imports: [embed]}
`
	files := map[string]string{
		"a/x.go":       "package a\n",
		"a/b/c/y.go":   "package c\n",
		"a/.hidden.go": "package a\n",
		"a/z.txt":      "x",
	}
	cases := map[string]struct {
		selector string
		want     []string
	}{
		"double star includes zero components": {"{kind: file, include: ['a/**/*.go']}", nil},
		"partially unmatched include": {"{kind: file, include: ['a/*.go', 'b/*.go']}",
			[]string{`select.include "b/*.go" matches no file`}},
		"type mismatch isn't a match": {"{kind: file, include: [a/b]}",
			[]string{`select.include "a/b" matches no file`}},
		"exclude removes every match": {"{kind: file, include: ['a/*.go'], exclude: ['a/*']}",
			[]string{`select.include "a/*.go" matches no file`}},
		"optional scope":         {"{kind: file, include: ['b/*.go'], on_empty: allow, empty_reason: Only some checkouts have b.}", nil},
		"unused exclude is fine": {"{kind: file, include: ['a/*.go'], exclude: [nothing/here]}", nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := evalRepo(t, with(files, map[string]string{"archtest.yaml": strings.Replace(base, "SELECT", tc.selector, 1)}))
			requireSet(t, got, tc.want...)
		})
	}
}

func TestExcludedRequirementIsAConflict(t *testing.T) {
	config := `version: 1
exclude: [{path: build, why: Output.}]
rules:
  - id: needs-build
    why: w
    select: {kind: directory, include: ['.']}
    tree: {require: {directories: [build]}}
  - id: reads-build
    why: w
    select: {kind: directory, include: [build]}
    tree: {forbid: [x]}
`
	got := evalRepo(t, map[string]string{"archtest.yaml": config, "build/out": "x"})
	requireSet(t, got,
		`archtest.yaml:10:13 [reads-build] repo.config_conflict: select.include "build" is excluded at the root`)
	config = strings.Replace(config, "  - id: reads-build\n    why: w\n    select: {kind: directory, include: [build]}\n    tree: {forbid: [x]}\n", "", 1)
	got = evalRepo(t, map[string]string{"archtest.yaml": config, "build/out": "x"})
	requireSet(t, got, `build [needs-build] repo.config_conflict: the required directory is excluded at the root`)
}

func TestGitIsExcludedButIgnoredFilesAreNot(t *testing.T) {
	config := `version: 1
rules:
  - id: root
    why: w
    select: {kind: directory, include: ['.']}
    tree: {allow: {files: [archtest.yaml, .gitignore]}}
`
	got := evalRepo(t, map[string]string{"archtest.yaml": config, ".gitignore": "secret\n", ".git/HEAD": "x", "secret": "x"})
	requireSet(t, got, "secret [root] repo.entry_unexpected")
}

func TestSymlinksAreUnsupported(t *testing.T) {
	dir := write(t, map[string]string{"archtest.yaml": `version: 1
rules:
  - id: root
    why: w
    select: {kind: directory, include: ['.']}
    tree: {require: {files: [target]}}
`, "real": "x"})
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "target")); err != nil {
		t.Skipf("can't create a symlink on this host, so link handling isn't tested here: %v", err)
	}
	requireSet(t, evalDir(t, dir, defaultLimits),
		"target repo.entry_unsupported",
		"target [root] repo.entry_missing")
}

func TestCaseCollisions(t *testing.T) {
	dir := write(t, map[string]string{"archtest.yaml": "version: 1\nrules:\n  - {id: r, why: w, select: {kind: directory, include: ['.']}, tree: {forbid: [x]}}\n", "a": "x"})
	if err := os.WriteFile(filepath.Join(dir, "A"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 3 {
		t.Skip("this file system is case-insensitive, so names can't collide here")
	}
	requireSet(t, evalDir(t, dir, defaultLimits), `a repo.case_collision: differs from "A" only in case`)
}

func TestLimits(t *testing.T) {
	files := with(profilesRepo, nil)
	dir := write(t, files)
	requireSet(t, evalDir(t, dir, limits{entries: 3, fileBytes: 1 << 20, totalBytes: 1 << 20}),
		"repo.limit_exceeded: the tree has more than 3 entries")
	requireSet(t, evalDir(t, dir, limits{entries: 100, fileBytes: 10, totalBytes: 1 << 20}),
		"config/profiles/default.yaml repo.limit_exceeded: is 34 bytes, more than the 10",
		"config/profiles/strict.json repo.limit_exceeded")
	requireSet(t, evalDir(t, dir, limits{entries: 100, fileBytes: 1 << 20, totalBytes: 40}),
		"config/profiles/strict.json repo.limit_exceeded: not checked: the run has already parsed 34 bytes")
}

func TestCancellationFailsTheRun(t *testing.T) {
	dir := write(t, profilesRepo)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ds, _, _ := evaluate(ctx, filepath.Join(dir, "archtest.yaml"), defaultLimits)
	got := formatAll(ds, "archtest.yaml")
	requireSet(t, got, ". repo.incomplete: stopped before checking this and 0 other directories: context canceled")
}

func TestUnreadableDirectoryIsIncomplete(t *testing.T) {
	dir := write(t, layoutRepo)
	locked := filepath.Join(dir, "docs", "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("this host can read a mode-0 directory, so permission errors aren't tested here")
	}
	requireSet(t, evalDir(t, dir, defaultLimits), "docs/locked repo.incomplete: can't read the directory")
}

// Output is identical across runs and across checkout locations, and checking
// changes no file.
func TestDeterministicOutput(t *testing.T) {
	broken := with(groupsRepo, map[string]string{
		"res/workflows/review.star":                 "x",
		"res/models/bundle.yaml":                    "x",
		"res/models/issue-triage/README.md":         "",
		"res/workflows/review/assets/a/bundle.yaml": "x",
	})
	first := evalRepo(t, broken)
	if len(first) != 4 {
		t.Fatalf("got %d problems, want 4:\n%s", len(first), strings.Join(first, "\n"))
	}
	dir := write(t, broken)
	before := snapshot(t, dir)
	for range 3 {
		if again := evalDir(t, dir, defaultLimits); !slices.Equal(first, again) {
			t.Fatalf("output changed:\n%s\nthen\n%s", strings.Join(first, "\n"), strings.Join(again, "\n"))
		}
	}
	if after := snapshot(t, dir); !maps.Equal(before, after) {
		t.Error("checking changed the repository")
	}
}

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := os.ReadFile(p)
			out[p] = string(data)
		}
		return nil
	})
	return out
}

func TestGlobs(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{".", ".", true},
		{".", "a", false},
		{"*", "a", true},
		{"*", "a/b", false},
		{"res/*", "res/a", true},
		{"res/*", "res/a/b", false},
		{"res/**/README.md", "res/README.md", true},
		{"res/**/README.md", "res/a/b/README.md", true},
		{"res/**", "res", true},
		{"res/**", "res/a/b", true},
		{"*/**/bundle.yaml", "bundle.yaml", false},
		{"*/**/bundle.yaml", "assets/bundle.yaml", true},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{"*.go", ".hidden.go", true},
		{"*.go", "x.Go", false},
		{"a*b*c", "aXbYbZc", true},
		{"a*b*c", "aXbYbZ", false},
	}
	for _, tc := range cases {
		if got := matchPath(tc.pattern, tc.path); got != tc.want {
			t.Errorf("matchPath(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

// A Windows junction redirects a directory like a symlink, and is treated the same.
func TestJunctionsAreUnsupported(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions exist only on Windows")
	}
	dir := write(t, map[string]string{"archtest.yaml": `version: 1
rules:
  - id: root
    why: w
    select: {kind: directory, include: ['.']}
    tree: {require: {directories: [link]}}
`, "real/a.txt": "x"})
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(dir, "link"), filepath.Join(dir, "real")).CombinedOutput(); err != nil {
		t.Skipf("can't create a junction here, so junction handling isn't tested: %v: %s", err, out)
	}
	requireSet(t, evalDir(t, dir, defaultLimits),
		"link repo.entry_unsupported",
		"link [root] repo.entry_missing")
}

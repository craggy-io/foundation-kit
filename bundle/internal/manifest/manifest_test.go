package manifest

import (
	"strings"
	"testing"
)

const valid = `apiVersion: craggy.io/bundle/v1
metadata:
  id: example.hello
  version: 0.1.0
  title: Hello
dependencies:
  - id: example.shared
    digest: sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
contributions:
  - id: hello
    kind: example.greeting@1
    path: greetings/hello.txt
    assets:
      - assets/tone.md
`

func TestParse(t *testing.T) {
	m, diags := Parse([]byte(valid))
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %+v", diags)
	}
	if m.Metadata.ID != "example.hello" || m.Metadata.Version != "0.1.0" || m.Metadata.Title != "Hello" {
		t.Errorf("Metadata = %+v", m.Metadata)
	}
	if len(m.Dependencies) != 1 || m.Dependencies[0].ID != "example.shared" {
		t.Errorf("Dependencies = %+v", m.Dependencies)
	}
	c := m.Contributions
	if len(c) != 1 || c[0].ID != "hello" || c[0].Kind != "example.greeting@1" || c[0].Path != "greetings/hello.txt" ||
		len(c[0].Assets) != 1 || c[0].Assets[0] != "assets/tone.md" || c[0].Line != 10 {
		t.Errorf("Contributions = %+v", c)
	}
}

func TestParseRejects(t *testing.T) {
	base := "apiVersion: craggy.io/bundle/v1\nmetadata: {id: a, version: '1'}\n"
	tests := []struct {
		name, src, code string
		line            int
	}{
		{"empty", "", codeInvalid, 1},
		{"not a mapping", "- a\n", codeInvalid, 1},
		{"syntax", "apiVersion: [\n", codeInvalid, 0},
		{"unknown field", base + "owner: me\n", codeInvalid, 3},
		{"wrong apiVersion", "apiVersion: v2\nmetadata: {id: a, version: '1'}\n", codeInvalid, 1},
		{"no apiVersion", "metadata: {id: a, version: '1'}\n", codeInvalid, 1},
		{"no metadata", "apiVersion: craggy.io/bundle/v1\n", codeInvalid, 1},
		{"no id", "apiVersion: craggy.io/bundle/v1\nmetadata: {version: '1'}\n", codeInvalid, 2},
		{"bad id", "apiVersion: craggy.io/bundle/v1\nmetadata: {id: 'a#b', version: '1'}\n", codeInvalid, 2},
		{"number version", "apiVersion: craggy.io/bundle/v1\nmetadata: {id: a, version: 1.0}\n", codeInvalid, 2},
		{"duplicate key", base + "metadata: {}\n", codeInvalid, 3},
		{"alias", "apiVersion: &v craggy.io/bundle/v1\nmetadata: {id: *v, version: '1'}\n", codeInvalid, 1},
		{"tag", base + "contributions: !!seq []\n", codeInvalid, 3},
		{"missing contribution fields", base + "contributions:\n  - {}\n", codeInvalid, 4},
		{"duplicate contribution", base + "contributions:\n  - {id: x, kind: a.b@1, path: a}\n  - {id: x, kind: a.b@1, path: b}\n", codeDuplicateID, 5},
		{"escaping path", base + "contributions:\n  - {id: x, kind: a.b@1, path: ../a}\n", codePathForbidden, 4},
		{"absolute path", base + "contributions:\n  - {id: x, kind: a.b@1, path: /a}\n", codePathForbidden, 4},
		{"dot path", base + "contributions:\n  - {id: x, kind: a.b@1, path: .}\n", codePathForbidden, 4},
		{"backslash path", base + "contributions:\n  - {id: x, kind: a.b@1, path: 'a\\b'}\n", codePathForbidden, 4},
		{"path in assets", base + "contributions:\n  - {id: x, kind: a.b@1, path: assets/a}\n", codePathForbidden, 4},
		{"path is assets", base + "contributions:\n  - {id: x, kind: a.b@1, path: assets}\n", codePathForbidden, 4},
		{"asset outside", base + "contributions:\n  - {id: x, kind: a.b@1, path: a, assets: [b]}\n", codePathForbidden, 4},
		{"asset escaping", base + "contributions:\n  - {id: x, kind: a.b@1, path: a, assets: [assets/../b]}\n", codePathForbidden, 4},
		{"asset twice", base + "contributions:\n  - {id: x, kind: a.b@1, path: a, assets: [assets/b, assets/b]}\n", codeDuplicateID, 4},
		{"self dependency", base + "dependencies: [{id: a}]\n", codeInvalid, 3},
		{"duplicate dependency", base + "dependencies: [{id: b}, {id: b}]\n", codeDuplicateID, 3},
		{"bad pin", base + "dependencies: [{id: b, digest: md5:00}]\n", codeInvalid, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := Parse([]byte(tt.src))
			if len(diags) == 0 {
				t.Fatal("no diagnostics")
			}
			for _, d := range diags {
				if d.Code == tt.code && (tt.line == 0 || d.Line == tt.line) {
					return
				}
			}
			t.Errorf("want %s on line %d, got %+v", tt.code, tt.line, diags)
		})
	}
}

func TestParseReportsEveryProblem(t *testing.T) {
	src := `apiVersion: v0
owner: me
metadata:
  id: a
contributions:
  - id: x
    kind: a.b@1
    path: ../x
  - id: x
    path: y
`
	_, diags := Parse([]byte(src))
	var msgs []string
	for _, d := range diags {
		msgs = append(msgs, d.Message)
	}
	// apiVersion, owner, version, ../x, missing kind, duplicate x.
	if len(diags) != 6 {
		t.Errorf("got %d diagnostics, want 6:\n%s", len(diags), strings.Join(msgs, "\n"))
	}
}

func TestParseAllowsEmptyLists(t *testing.T) {
	src := "apiVersion: craggy.io/bundle/v1\nmetadata: {id: a, version: '1'}\ndependencies:\ncontributions: []\n"
	if _, diags := Parse([]byte(src)); len(diags) != 0 {
		t.Errorf("diagnostics = %+v", diags)
	}
}

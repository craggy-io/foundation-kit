package compile

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/craggy-io/foundation-kit/bundle"
)

type profile struct {
	Retries int `json:"retries"`
}

func contribution(content string) bundle.Contribution {
	return bundle.Contribution{
		Path:  "file",
		Files: fstest.MapFS{"file": {Data: []byte(content)}},
	}
}

func requireFailed(t *testing.T, diags []bundle.Diagnostic) {
	t.Helper()
	if len(diags) != 1 || diags[0].Code != bundle.CodeCompileFailed || diags[0].Severity != bundle.SeverityError {
		t.Errorf("diagnostics = %+v, want one compile failure", diags)
	}
}

func TestText(t *testing.T) {
	out, diags := Text()(context.Background(), contribution("hello"))
	if len(diags) != 0 || out.Value != "hello" {
		t.Errorf("Text = %q, %+v", out.Value, diags)
	}
}

func TestMissingFile(t *testing.T) {
	c := contribution("")
	c.Path = "missing"
	_, diags := Text()(context.Background(), c)
	requireFailed(t, diags)
	_, diags = JSON[profile]()(context.Background(), c)
	requireFailed(t, diags)
	_, diags = YAML[profile]()(context.Background(), c)
	requireFailed(t, diags)
}

func TestJSON(t *testing.T) {
	out, diags := JSON[profile]()(context.Background(), contribution(`{"retries": 3}`))
	if len(diags) != 0 || out.Value.Retries != 3 {
		t.Errorf("JSON = %+v, %+v", out.Value, diags)
	}
}

func TestJSONRejects(t *testing.T) {
	for name, content := range map[string]string{
		"malformed":     `{`,
		"unknown field": `{"retries": 3, "other": 1}`,
		"wrong type":    `{"retries": "three"}`,
		"trailing data": `{"retries": 3} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, diags := JSON[profile]()(context.Background(), contribution(content))
			requireFailed(t, diags)
		})
	}
}

func TestYAML(t *testing.T) {
	out, diags := YAML[profile]()(context.Background(), contribution("# A comment.\nretries: 3\n"))
	if len(diags) != 0 || out.Value.Retries != 3 {
		t.Errorf("YAML = %+v, %+v", out.Value, diags)
	}
}

func TestYAMLUsesJSONTags(t *testing.T) {
	type tagged struct {
		MaxRetries int64    `json:"max_retries"`
		Ratio      float64  `json:"ratio"`
		Names      []string `json:"names"`
		On         bool     `json:"on"`
		Missing    *string  `json:"missing"`
		When       string   `json:"when"`
	}
	src := "max_retries: 9007199254740993\nratio: 0.5\nnames: [a, b]\non: true\nmissing: null\nwhen: 2026-11-05\n"
	out, diags := YAML[tagged]()(context.Background(), contribution(src))
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %+v", diags)
	}
	got := out.Value
	if got.MaxRetries != 9007199254740993 || got.Ratio != 0.5 || len(got.Names) != 2 || !got.On || got.Missing != nil || got.When != "2026-11-05" {
		t.Errorf("YAML = %+v", got)
	}
}

func TestYAMLRejects(t *testing.T) {
	tests := map[string]struct {
		content  string
		wantLine int
	}{
		"malformed":       {"retries: [", 0},
		"empty":           {"", 1},
		"unknown field":   {"retries: 3\nother: 1\n", 2},
		"wrong type":      {"retries: three\n", 1},
		"duplicate key":   {"retries: 3\nretries: 4\n", 2},
		"anchor":          {"a: &x 1\nretries: *x\n", 0},
		"merge key":       {"base: {retries: 3}\n<<: {retries: 4}\n", 2},
		"custom tag":      {"retries: !custom 3\n", 1},
		"explicit tag":    {"retries: !!int 3\n", 1},
		"two documents":   {"retries: 3\n---\nretries: 4\n", 0},
		"non-string key":  {"1: 3\n", 1},
		"no JSON form":    {"retries: .inf\n", 1},
		"top-level array": {"- 1\n", 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, diags := YAML[profile]()(context.Background(), contribution(tt.content))
			if len(diags) == 0 {
				t.Fatal("no diagnostics")
			}
			for _, d := range diags {
				if d.Code != bundle.CodeCompileFailed || d.Severity != bundle.SeverityError {
					t.Errorf("diagnostic = %+v, want a compile failure", d)
				}
			}
			if tt.wantLine != 0 && diags[0].Line != tt.wantLine {
				t.Errorf("line = %d, want %d (%s)", diags[0].Line, tt.wantLine, diags[0].Message)
			}
		})
	}
}

func TestYAMLReportsEveryProblem(t *testing.T) {
	_, diags := YAML[profile]()(context.Background(), contribution("a: 1\na: 2\nb: !x 3\n"))
	if len(diags) != 2 {
		t.Errorf("diagnostics = %+v, want 2", diags)
	}
}

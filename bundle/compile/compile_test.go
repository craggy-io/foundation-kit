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

func TestYAMLReportsThatItIsNotImplemented(t *testing.T) {
	_, diags := YAML[profile]()(context.Background(), contribution("retries: 3"))
	requireFailed(t, diags)
}

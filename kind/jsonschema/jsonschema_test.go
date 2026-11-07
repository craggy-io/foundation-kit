package jsonschema_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/craggy-io/foundation-kit/bundle/bundletest"
	"github.com/craggy-io/foundation-kit/kind"
	"github.com/craggy-io/foundation-kit/kind/jsonschema"
)

type profile struct {
	Name    string `json:"name"`
	Retries int    `json:"retries"`
}

const profileSchema = `{
	"type": "object",
	"properties": {
		"name": {"type": "string", "minLength": 1},
		"retries": {"type": "integer", "minimum": 0}
	},
	"required": ["name", "retries"],
	"additionalProperties": false
}`

func TestNewWithKind(t *testing.T) {
	s, err := jsonschema.New([]byte(profileSchema))
	if err != nil {
		t.Fatal(err)
	}
	k := kind.New[profile]("example.profile", 1, kind.WithSchema(s))
	bundletest.CheckSample(t, k, profile{Name: "default", Retries: 3})

	if err := k.Validate(profile{Name: "", Retries: -1}); err == nil {
		t.Error("Validate accepted a value the schema forbids")
	}
}

func TestNewRejects(t *testing.T) {
	for name, doc := range map[string]string{
		"malformed":       `{`,
		"invalid schema":  `{"type": 3}`,
		"external ref":    `{"$ref": "other.json"}`,
		"remote ref":      `{"$ref": "https://example.com/schema.json"}`,
		"trailing data":   `{} {}`,
		"unknown keyword": `{"type": "nonsense"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := jsonschema.New([]byte(doc)); err == nil {
				t.Error("New succeeded")
			}
		})
	}
}

func TestProblems(t *testing.T) {
	s, err := jsonschema.New([]byte(profileSchema))
	if err != nil {
		t.Fatal(err)
	}
	err = s.Validate(map[string]any{"retries": -1, "extra": true})
	got, ok := jsonschema.Problems(err)
	if !ok {
		t.Fatalf("Problems(%v) reported false", err)
	}
	pointers := make([]string, len(got))
	for i, p := range got {
		pointers[i] = p.Pointer
		if p.Message == "" {
			t.Errorf("problem at %q has no message", p.Pointer)
		}
	}
	if want := []string{"", "", "/retries"}; !slices.Equal(pointers, want) {
		t.Errorf("pointers = %q, want %q (%+v)", pointers, want, got)
	}
	if _, ok := jsonschema.Problems(errors.New("other")); ok {
		t.Error("Problems accepted an unrelated error")
	}
	if _, ok := jsonschema.Problems(nil); ok {
		t.Error("Problems accepted nil")
	}
}

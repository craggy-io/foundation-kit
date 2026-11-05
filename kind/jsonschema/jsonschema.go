// Package jsonschema provides a kind.Schema backed by JSON Schema. It is separate
// from package kind so that only callers who use it compile its dependency.
package jsonschema

import (
	"bytes"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/craggy-io/foundation-kit/kind"
)

// resource is the URL the document is compiled under. Nothing is fetched from it.
const resource = "mem:///schema.json"

// New compiles a JSON Schema document into a kind.Schema. The document's $schema
// selects the draft, and draft 2020-12 is used if it has none.
//
// Compiling does no I/O: a $ref to anything outside the document is an error, so a
// schema can't change because a file or URL did. Its Validate method accepts the
// JSON form described on kind.Schema, including json.Number, and is safe for
// concurrent use.
func New(document []byte) (kind.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(document))
	if err != nil {
		return nil, fmt.Errorf("jsonschema: parsing the document: %w", err)
	}
	c := jsonschema.NewCompiler()
	c.UseLoader(jsonschema.SchemeURLLoader{}) // no file or network access
	if err := c.AddResource(resource, doc); err != nil {
		return nil, fmt.Errorf("jsonschema: %w", err)
	}
	s, err := c.Compile(resource)
	if err != nil {
		return nil, fmt.Errorf("jsonschema: compiling the document: %w", err)
	}
	return schema{s}, nil
}

type schema struct{ s *jsonschema.Schema }

func (s schema) Validate(value any) error { return s.s.Validate(value) }

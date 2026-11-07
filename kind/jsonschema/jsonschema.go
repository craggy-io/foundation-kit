// Package jsonschema provides a kind.Schema backed by JSON Schema. It is separate
// from package kind so that only callers who use it compile its dependency.
package jsonschema

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

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

// Problem is one reason a value failed validation.
type Problem struct {
	// Pointer is the JSON Pointer (RFC 6901) of the failing value within the
	// validated value. It is "" for the value itself.
	Pointer string

	// Message says what is wrong, in English.
	Message string
}

var printer = message.NewPrinter(language.English)

// Problems splits an error returned by the Validate method of a schema from New into
// its individual problems, sorted by Pointer and then Message. It reports false for
// any other error, including nil.
//
// It exists so that a caller can position each problem in the source text it decoded
// the value from, without depending on the JSON Schema library's types.
func Problems(err error) ([]Problem, bool) {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return nil, false
	}
	var out []Problem
	var walk func(*jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			var ptr strings.Builder
			for _, tok := range e.InstanceLocation {
				ptr.WriteByte('/')
				ptr.WriteString(strings.NewReplacer("~", "~0", "/", "~1").Replace(tok))
			}
			out = append(out, Problem{Pointer: ptr.String(), Message: e.ErrorKind.LocalizedString(printer)})
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	slices.SortFunc(out, func(a, b Problem) int {
		return cmp.Or(cmp.Compare(a.Pointer, b.Pointer), cmp.Compare(a.Message, b.Message))
	})
	return slices.Compact(out), true
}

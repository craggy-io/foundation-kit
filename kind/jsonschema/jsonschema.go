// Package jsonschema provides a kind.Schema backed by JSON Schema. It is separate
// from package kind so that only callers who use it compile its dependency.
package jsonschema

import (
	"errors"

	"github.com/craggy-io/foundation-kit/kind"
)

// ErrNotImplemented is returned until this package has an implementation.
//
// TODO: delete this once New is implemented.
var ErrNotImplemented = errors.New("jsonschema: not implemented")

// New compiles a JSON Schema document into a kind.Schema.
//
// TODO: implement. data-services already depends on
// github.com/santhosh-tekuri/jsonschema/v6, so consider that first. It must accept the
// JSON form described on kind.Schema, including json.Number.
func New(document []byte) (kind.Schema, error) {
	return nil, ErrNotImplemented
}

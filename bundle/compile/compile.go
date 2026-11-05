// Package compile provides ready-made compilers for kinds whose contributions are a
// single file. A kind with extra rules can wrap one of these or write its own.
package compile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/craggy-io/foundation-kit/bundle"
)

// Text returns a Compiler whose value is the file's contents as a string.
func Text() bundle.Compiler[string] {
	return func(ctx context.Context, c bundle.Contribution) (bundle.Compiled[string], []bundle.Diagnostic) {
		data, err := fs.ReadFile(c.Files, c.Path)
		if err != nil {
			return bundle.Compiled[string]{}, failed(c, err)
		}
		return bundle.Compiled[string]{Value: string(data)}, nil
	}
}

// JSON returns a Compiler that decodes the file into T. Unknown fields and trailing
// data are errors.
//
// It does not detect duplicate keys, which encoding/json accepts, and its diagnostics
// have no line numbers.
func JSON[T any]() bundle.Compiler[T] {
	return func(ctx context.Context, c bundle.Contribution) (bundle.Compiled[T], []bundle.Diagnostic) {
		var out bundle.Compiled[T]
		data, err := fs.ReadFile(c.Files, c.Path)
		if err != nil {
			return out, failed(c, err)
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&out.Value); err != nil {
			return bundle.Compiled[T]{}, failed(c, err)
		}
		if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
			return bundle.Compiled[T]{}, failed(c, errors.New("unexpected data after the JSON value"))
		}
		return out, nil
	}
}

// YAML returns a Compiler that decodes the file into T under the same strict rules
// as bundle.yaml.
//
// TODO: implement once internal/manifest has chosen a YAML library, and share its
// strict parsing. Until then, the Compiler reports a compile failure for every file.
func YAML[T any]() bundle.Compiler[T] {
	return func(ctx context.Context, c bundle.Contribution) (bundle.Compiled[T], []bundle.Diagnostic) {
		return bundle.Compiled[T]{}, failed(c, fmt.Errorf("compile.YAML: %w", bundle.ErrNotImplemented))
	}
}

func failed(c bundle.Contribution, err error) []bundle.Diagnostic {
	return []bundle.Diagnostic{{
		Code:         bundle.CodeCompileFailed,
		Severity:     bundle.SeverityError,
		Bundle:       c.Bundle.ID,
		Contribution: c.Ref.ID,
		Path:         c.Path,
		Message:      err.Error(),
	}}
}

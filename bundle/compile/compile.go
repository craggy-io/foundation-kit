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
	"strings"

	"github.com/craggy-io/foundation-kit/bundle"
	"github.com/craggy-io/foundation-kit/bundle/internal/strictyaml"
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
// as bundle.yaml: duplicate keys, anchors, aliases, merge keys, explicit tags and
// more than one document are errors, each reported with its line.
//
// The checked document is then decoded like a JSON file, so T's json struct tags
// apply and unknown fields are errors, exactly as for JSON. One Go type can therefore
// be read from either format, and is validated by the same kind schema. Timestamps
// are read as strings, and numbers with no JSON form, such as .inf, are errors.
func YAML[T any]() bundle.Compiler[T] {
	return func(ctx context.Context, c bundle.Contribution) (bundle.Compiled[T], []bundle.Diagnostic) {
		var out bundle.Compiled[T]
		data, err := fs.ReadFile(c.Files, c.Path)
		if err != nil {
			return out, failed(c, err)
		}
		root, yamlDiags := strictyaml.Parse(data)
		if len(yamlDiags) == 0 && root == nil {
			yamlDiags = []strictyaml.Diagnostic{{Line: 1, Column: 1, Message: "the file is empty"}}
		}
		if len(yamlDiags) == 0 {
			var doc []byte
			doc, yamlDiags = root.JSON()
			if len(yamlDiags) == 0 {
				dec := json.NewDecoder(bytes.NewReader(doc))
				dec.DisallowUnknownFields()
				if err := dec.Decode(&out.Value); err != nil {
					return bundle.Compiled[T]{}, []bundle.Diagnostic{positioned(c, root, err)}
				}
				return out, nil
			}
		}
		diags := make([]bundle.Diagnostic, 0, len(yamlDiags))
		for _, d := range yamlDiags {
			diag := failed(c, errors.New(d.Message))[0]
			diag.Line, diag.Column = d.Line, d.Column
			diags = append(diags, diag)
		}
		return bundle.Compiled[T]{}, diags
	}
}

// positioned turns a JSON decoding error into a diagnostic, finding the line of the
// field it names where it can. The search is by field name, so it is a best effort
// when the same name appears more than once.
func positioned(c bundle.Contribution, root *strictyaml.Node, err error) bundle.Diagnostic {
	d := failed(c, err)[0]
	d.Message = strings.TrimPrefix(err.Error(), "json: ")
	var field string
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &typeErr):
		field = typeErr.Field
		if i := strings.LastIndex(field, "."); i >= 0 {
			field = field[i+1:]
		}
		d.Message = fmt.Sprintf("field %s: cannot use %s as %s", typeErr.Field, typeErr.Value, typeErr.Type)
	case strings.HasPrefix(err.Error(), `json: unknown field "`):
		field = strings.TrimSuffix(strings.TrimPrefix(err.Error(), `json: unknown field "`), `"`)
	}
	if field != "" {
		if pair, ok := root.Find(field); ok {
			d.Line, d.Column = pair.Line, pair.Column
		}
	}
	return d
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

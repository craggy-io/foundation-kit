package archtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/craggy-io/foundation-kit/internal/strictyaml"
	"github.com/craggy-io/foundation-kit/kind"
	"github.com/craggy-io/foundation-kit/kind/jsonschema"
)

// document is a parsed JSON or YAML value with the source position of every value,
// keyed by JSON Pointer.
type document struct {
	value     any
	positions map[string]pos
	problems  []diagnostic // syntax problems; value is unusable if there are any
}

// checkStructured parses one JSON or YAML file and validates it against a schema.
func (r *repo) checkStructured(s *structuredCheck, e *entry) []diagnostic {
	format := s.Format
	if format == formatAuto {
		switch path.Ext(e.name) {
		case ".json":
			format = formatJSON
		case ".yaml", ".yml":
			format = formatYAML
		default:
			return []diagnostic{{Code: codeConfigInvalid, Path: e.path,
				Message: "format: auto knows only .json, .yaml and .yml; set format, or narrow the selector"}}
		}
	}
	key := format + ":" + e.path
	doc, cached := r.parsed[key].(*document)
	if !cached {
		data, ok := r.readFile(e)
		if !ok {
			return nil
		}
		if format == formatJSON {
			doc = parseJSON(data)
		} else {
			doc = parseYAML(data, 0)
		}
		r.parsed[key] = doc
	}
	var ds []diagnostic
	for _, p := range doc.problems {
		p.Path = e.path
		ds = append(ds, p)
	}
	if len(ds) > 0 {
		return ds
	}
	return validate(s.Schema.compiled, doc, e.path)
}

// validate checks a parsed value, positioning each problem at the nearest value the
// source has a position for.
func validate(schema kind.Schema, doc *document, file string) []diagnostic {
	err := schema.Validate(doc.value)
	if err == nil {
		return nil
	}
	problems, ok := jsonschema.Problems(err)
	if !ok {
		return []diagnostic{{Code: codeSchemaInvalid, Path: file, Message: err.Error()}}
	}
	ds := make([]diagnostic, 0, len(problems))
	for _, p := range problems {
		at := nearest(doc.positions, p.Pointer)
		ds = append(ds, diagnostic{Code: codeSchemaInvalid, Path: file, Line: at.line, Column: at.column,
			Pointer: p.Pointer, Message: p.Message})
	}
	return ds
}

// nearest returns the position of pointer, or of its closest ancestor with one.
func nearest(positions map[string]pos, pointer string) pos {
	for {
		if p, ok := positions[pointer]; ok {
			return p
		}
		i := strings.LastIndexByte(pointer, '/')
		if i < 0 {
			return pos{}
		}
		pointer = pointer[:i]
	}
}

func escapePointer(token string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(token)
}

func checkText(data []byte) *diagnostic {
	if !utf8.Valid(data) {
		return &diagnostic{Code: codeParseFailed, Message: "isn't valid UTF-8"}
	}
	return nil
}

// parseYAML parses the strict YAML subset. lineOffset is added to every line, for
// YAML embedded in another file.
func parseYAML(data []byte, lineOffset int) *document {
	doc := &document{positions: map[string]pos{}}
	if d := checkText(data); d != nil {
		doc.problems = []diagnostic{*d}
		return doc
	}
	root, diags := strictyaml.Parse(data)
	for _, y := range diags {
		line := y.Line
		if line > 0 {
			line += lineOffset
		}
		doc.problems = append(doc.problems, diagnostic{Code: codeParseFailed, Line: line, Column: y.Column, Message: y.Message})
	}
	if len(diags) > 0 {
		return doc
	}
	if root == nil {
		doc.problems = []diagnostic{{Code: codeParseFailed, Message: "is empty"}}
		return doc
	}
	var walk func(n *strictyaml.Node, ptr string)
	walk = func(n *strictyaml.Node, ptr string) {
		doc.positions[ptr] = pos{n.Line + lineOffset, n.Column}
		for _, p := range n.Pairs {
			walk(p.Value, ptr+"/"+escapePointer(p.Key))
		}
		for i, item := range n.Items {
			walk(item, ptr+"/"+strconv.Itoa(i))
		}
	}
	walk(root, "")
	data, jsonDiags := root.JSON()
	for _, y := range jsonDiags {
		doc.problems = append(doc.problems, diagnostic{Code: codeParseFailed, Line: y.Line + lineOffset, Column: y.Column, Message: y.Message})
	}
	if len(jsonDiags) > 0 {
		return doc
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&doc.value); err != nil {
		doc.problems = []diagnostic{{Code: codeParseFailed, Message: err.Error()}}
	}
	return doc
}

// parseJSON parses one JSON value, rejecting duplicate keys and trailing data, and
// keeping numbers exact.
func parseJSON(data []byte) *document {
	doc := &document{positions: map[string]pos{}}
	if d := checkText(data); d != nil {
		doc.problems = []diagnostic{*d}
		return doc
	}
	lines := newLines(data)
	if len(bytes.TrimSpace(data)) == 0 {
		doc.problems = []diagnostic{{Code: codeParseFailed, Message: "is empty"}}
		return doc
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	fail := func(err error) {
		offset := int(dec.InputOffset())
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			offset = max(int(syn.Offset)-1, 0) // Offset counts the offending byte
		} else if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			offset, err = len(data), errors.New("unexpected end of JSON input")
		}
		p := lines.pos(offset)
		doc.problems = append(doc.problems, diagnostic{Code: codeParseFailed, Line: p.line, Column: p.column, Message: err.Error()})
	}

	var parse func(ptr string) (any, bool)
	parse = func(ptr string) (any, bool) {
		doc.positions[ptr] = lines.pos(valueStart(data, int(dec.InputOffset())))
		tok, err := dec.Token()
		if err != nil {
			fail(err)
			return nil, false
		}
		delim, isDelim := tok.(json.Delim)
		if !isDelim {
			return tok, true
		}
		switch delim {
		case '{':
			m := map[string]any{}
			for dec.More() {
				keyAt := lines.pos(valueStart(data, int(dec.InputOffset())))
				kt, err := dec.Token()
				if err != nil {
					fail(err)
					return nil, false
				}
				key, _ := kt.(string)
				if _, dup := m[key]; dup {
					doc.problems = append(doc.problems, diagnostic{Code: codeParseFailed, Line: keyAt.line, Column: keyAt.column,
						Message: fmt.Sprintf("duplicate key %q", key)})
				}
				v, ok := parse(ptr + "/" + escapePointer(key))
				if !ok {
					return nil, false
				}
				m[key] = v
			}
			if _, err := dec.Token(); err != nil {
				fail(err)
				return nil, false
			}
			return m, true
		case '[':
			s := []any{}
			for dec.More() {
				v, ok := parse(ptr + "/" + strconv.Itoa(len(s)))
				if !ok {
					return nil, false
				}
				s = append(s, v)
			}
			if _, err := dec.Token(); err != nil {
				fail(err)
				return nil, false
			}
			return s, true
		}
		fail(fmt.Errorf("unexpected %v", delim))
		return nil, false
	}
	v, ok := parse("")
	if !ok {
		return doc
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		p := lines.pos(valueStart(data, int(dec.InputOffset())))
		doc.problems = append(doc.problems, diagnostic{Code: codeParseFailed, Line: p.line, Column: p.column,
			Message: "only one JSON value is allowed"})
	}
	doc.value = v
	return doc
}

// valueStart skips the whitespace and separators before the next JSON token.
func valueStart(data []byte, offset int) int {
	for offset < len(data) {
		switch data[offset] {
		case ' ', '\t', '\r', '\n', ',', ':':
			offset++
		default:
			return offset
		}
	}
	return offset
}

// lines maps byte offsets to 1-based line and column positions. Columns count
// runes, and a "\r\n" ending is one line break.
type lines struct {
	data   []byte
	starts []int
}

func newLines(data []byte) lines {
	starts := []int{0}
	for i, c := range data {
		if c == '\n' {
			starts = append(starts, i+1)
		}
	}
	return lines{data: data, starts: starts}
}

func (l lines) pos(offset int) pos {
	lo, hi := 0, len(l.starts)
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if l.starts[mid] <= offset {
			lo = mid
		} else {
			hi = mid
		}
	}
	offset = min(offset, len(l.data))
	return pos{lo + 1, utf8.RuneCount(l.data[l.starts[lo]:offset]) + 1}
}

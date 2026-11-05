// Package strictyaml parses the restricted, JSON-compatible subset of YAML that
// bundle.yaml and compile.YAML accept. It is the only place the YAML library is
// imported, so the library can be swapped without changing any other package.
//
// The subset rejects duplicate keys, anchors, aliases, merge keys, explicit tags,
// non-string mapping keys and more than one document. Every problem in a document is
// reported, each with its line and column.
package strictyaml

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// Diagnostic is one problem, positioned in the source text. Line and Column are
// 1-based, and zero when unknown.
type Diagnostic struct {
	Line    int
	Column  int
	Message string
}

// Kind says what a Node holds.
type Kind int

const (
	Null Kind = iota
	String
	Number
	Bool
	Mapping
	Sequence
)

// String names the kind for diagnostics.
func (k Kind) String() string {
	switch k {
	case Null:
		return "null"
	case String:
		return "a string"
	case Number:
		return "a number"
	case Bool:
		return "a boolean"
	case Mapping:
		return "a mapping"
	case Sequence:
		return "a sequence"
	}
	return "unknown"
}

// Node is one checked value. It holds no type from the YAML library.
type Node struct {
	Kind   Kind
	Line   int
	Column int

	// Value is the scalar's text, for String, Number and Bool.
	Value string

	// Pairs holds a Mapping's entries in source order. Keys are unique.
	Pairs []Pair

	// Items holds a Sequence's elements.
	Items []*Node

	// json is a Number's or Bool's JSON form, or empty if it has none.
	json string
}

// Pair is one mapping entry.
type Pair struct {
	Key    string
	Line   int
	Column int
	Value  *Node
}

var lineInError = regexp.MustCompile(`^yaml: line (\d+): `)

// Parse checks data and returns its single document. It returns a nil Node with no
// diagnostics for an empty document. If any diagnostic is returned the Node is nil.
func Parse(data []byte) (*Node, []Diagnostic) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, []Diagnostic{syntaxError(err)}
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, []Diagnostic{syntaxError(err)}
		}
		return nil, []Diagnostic{{Line: extra.Line, Column: extra.Column, Message: "only one YAML document is allowed"}}
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	var diags []Diagnostic
	n := convert(doc.Content[0], &diags)
	if len(diags) > 0 {
		return nil, diags
	}
	return n, nil
}

func syntaxError(err error) Diagnostic {
	msg := err.Error()
	if m := lineInError.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		return Diagnostic{Line: line, Message: msg[len(m[0]):]}
	}
	if len(msg) > 6 && msg[:6] == "yaml: " {
		msg = msg[6:]
	}
	return Diagnostic{Message: msg}
}

func convert(y *yaml.Node, diags *[]Diagnostic) *Node {
	report := func(format string, args ...any) {
		*diags = append(*diags, Diagnostic{Line: y.Line, Column: y.Column, Message: fmt.Sprintf(format, args...)})
	}
	if y.Kind == yaml.AliasNode {
		report("aliases are not allowed")
		return nil
	}
	if y.Anchor != "" {
		report("anchors are not allowed")
	}
	if y.Style&yaml.TaggedStyle != 0 {
		report("tags are not allowed, but found %s", y.Tag)
		return nil
	}

	n := &Node{Line: y.Line, Column: y.Column}
	switch y.Kind {
	case yaml.ScalarNode:
		n.Value = y.Value
		switch y.ShortTag() {
		case "!!str", "!!timestamp":
			n.Kind = String
		case "!!int", "!!float":
			n.Kind = Number
			n.json = jsonNumber(y)
		case "!!bool":
			n.Kind = Bool
			var b bool
			if err := y.Decode(&b); err != nil {
				report("%v", err)
				return nil
			}
			n.json = strconv.FormatBool(b)
		case "!!null":
			n.Kind = Null
		default:
			report("unsupported value of type %s", y.ShortTag())
			return nil
		}
	case yaml.MappingNode:
		n.Kind = Mapping
		seen := map[string]int{}
		for i := 0; i+1 < len(y.Content); i += 2 {
			k, v := y.Content[i], y.Content[i+1]
			if k.Kind == yaml.ScalarNode && k.ShortTag() == "!!merge" {
				*diags = append(*diags, Diagnostic{Line: k.Line, Column: k.Column, Message: "merge keys are not allowed"})
				continue
			}
			if k.Kind != yaml.ScalarNode || k.ShortTag() != "!!str" || k.Style&yaml.TaggedStyle != 0 || k.Anchor != "" {
				*diags = append(*diags, Diagnostic{Line: k.Line, Column: k.Column, Message: "mapping keys must be plain strings"})
				continue
			}
			if first, dup := seen[k.Value]; dup {
				*diags = append(*diags, Diagnostic{Line: k.Line, Column: k.Column,
					Message: fmt.Sprintf("duplicate key %q, first defined on line %d", k.Value, first)})
				continue
			}
			seen[k.Value] = k.Line
			if value := convert(v, diags); value != nil {
				n.Pairs = append(n.Pairs, Pair{Key: k.Value, Line: k.Line, Column: k.Column, Value: value})
			}
		}
	case yaml.SequenceNode:
		n.Kind = Sequence
		for _, item := range y.Content {
			if value := convert(item, diags); value != nil {
				n.Items = append(n.Items, value)
			}
		}
	default:
		report("unsupported YAML node")
		return nil
	}
	return n
}

// Find returns the first pair in a Mapping whose key is key, searching the whole tree
// depth first. It is used to give a line number to a decoding error that names only
// a field.
func (n *Node) Find(key string) (Pair, bool) {
	if n == nil {
		return Pair{}, false
	}
	for _, p := range n.Pairs {
		if p.Key == key {
			return p, true
		}
		if found, ok := p.Value.Find(key); ok {
			return found, true
		}
	}
	for _, item := range n.Items {
		if found, ok := item.Find(key); ok {
			return found, true
		}
	}
	return Pair{}, false
}

// JSON returns the node's value encoded as JSON, so that it can be decoded with the
// same rules and struct tags as a JSON file.
func (n *Node) JSON() ([]byte, []Diagnostic) {
	var diags []Diagnostic
	v := n.value(&diags)
	if len(diags) > 0 {
		return nil, diags
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, []Diagnostic{{Message: err.Error()}}
	}
	return data, nil
}

func (n *Node) value(diags *[]Diagnostic) any {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case String:
		return n.Value
	case Bool:
		return n.json == "true"
	case Number:
		if n.json == "" {
			*diags = append(*diags, Diagnostic{Line: n.Line, Column: n.Column,
				Message: fmt.Sprintf("number %s has no JSON form", n.Value)})
			return nil
		}
		return json.Number(n.json)
	case Mapping:
		m := make(map[string]any, len(n.Pairs))
		for _, p := range n.Pairs {
			m[p.Key] = p.Value.value(diags)
		}
		return m
	case Sequence:
		s := make([]any, 0, len(n.Items))
		for _, item := range n.Items {
			s = append(s, item.value(diags))
		}
		return s
	}
	return nil
}

// jsonNumber returns a number's JSON form, or "" if it has none. Integers stay exact
// rather than being rounded through float64.
func jsonNumber(y *yaml.Node) string {
	var i int64
	if y.ShortTag() == "!!int" && y.Decode(&i) == nil {
		return strconv.FormatInt(i, 10)
	}
	var u uint64
	if y.ShortTag() == "!!int" && y.Decode(&u) == nil {
		return strconv.FormatUint(u, 10)
	}
	var f float64
	if err := y.Decode(&f); err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return ""
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

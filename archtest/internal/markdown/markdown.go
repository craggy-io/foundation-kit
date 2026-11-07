// Package markdown reads the heading structure of a CommonMark document. It is the
// only place archtest imports a Markdown parser, so the parser can be replaced
// without changing anything else, and none of its types leave this package.
//
// Headings are found by parsing the document, so a "#" line inside a code fence is
// not a heading. Only headings at the top level of the document start sections; one
// inside a list or block quote is content.
package markdown

import (
	"bytes"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Heading is one top-level ATX or setext heading.
type Heading struct {
	Level int

	// Text is the heading's rendered inline text, without formatting markers, with
	// runs of whitespace collapsed to one space and the ends trimmed.
	Text string

	// Line is the 1-based line the heading's text is on, or 0 if it has no text.
	Line int

	// HasBody reports whether the heading's section holds content: a block other
	// than a heading, an HTML comment or a thematic break, with non-whitespace text.
	// The section runs to the next heading of the same or a lower level.
	HasBody bool
}

// Headings returns the document's top-level headings in order. src must use "\n"
// line endings.
func Headings(src []byte) []Heading {
	doc := goldmark.New().Parser().Parse(text.NewReader(src))
	lines := newLineIndex(src)

	type found struct {
		node ast.Node
		h    Heading
	}
	var all []found
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		h, ok := n.(*ast.Heading)
		if !ok {
			continue
		}
		heading := Heading{Level: h.Level, Text: collapse(inlineText(h, src))}
		if h.Lines().Len() > 0 {
			heading.Line = lines.line(h.Lines().At(0).Start)
		}
		all = append(all, found{node: n, h: heading})
	}

	out := make([]Heading, len(all))
	for i, f := range all {
		for n := f.node.NextSibling(); n != nil; n = n.NextSibling() {
			if h, ok := n.(*ast.Heading); ok {
				if h.Level <= f.h.Level {
					break
				}
				continue
			}
			if hasContent(n, src) {
				f.h.HasBody = true
				break
			}
		}
		out[i] = f.h
	}
	return out
}

// inlineText concatenates the text of a node's inline descendants.
func inlineText(n ast.Node, src []byte) string {
	var b strings.Builder
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			switch c := c.(type) {
			case *ast.Text:
				b.Write(c.Segment.Value(src))
				if c.SoftLineBreak() || c.HardLineBreak() {
					b.WriteByte(' ')
				}
			case *ast.String:
				b.Write(c.Value)
			case *ast.AutoLink:
				b.Write(c.Label(src))
			case *ast.RawHTML:
				// Markup isn't text.
			default:
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// hasContent reports whether a block holds non-whitespace content.
func hasContent(n ast.Node, src []byte) bool {
	switch n := n.(type) {
	case *ast.ThematicBreak:
		return false
	case *ast.HTMLBlock:
		if n.HTMLBlockType == ast.HTMLBlockType2 { // <!-- comment -->
			return false
		}
	}
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		if len(bytes.TrimFunc(seg.Value(src), unicode.IsSpace)) > 0 {
			return true
		}
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if c.Type() == ast.TypeInline {
			// A paragraph's text is in its Lines, so inlines add nothing new.
			continue
		}
		if hasContent(c, src) {
			return true
		}
	}
	return false
}

// lineIndex maps byte offsets to 1-based line numbers.
type lineIndex []int

func newLineIndex(src []byte) lineIndex {
	starts := lineIndex{0}
	for i, c := range src {
		if c == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func (l lineIndex) line(offset int) int {
	lo, hi := 0, len(l)
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if l[mid] <= offset {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo + 1
}

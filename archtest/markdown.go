package archtest

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/craggy-io/foundation-kit/archtest/internal/markdown"
)

// markdownDoc is a parsed Markdown file.
type markdownDoc struct {
	problems    []diagnostic // problems that stop every check
	frontmatter *document    // nil if the file has none
	fmProblem   *diagnostic  // a malformed frontmatter block
	headings    []markdown.Heading
}

// parseMarkdown splits off frontmatter and reads the heading structure. CRLF and LF
// endings are equivalent, and a leading UTF-8 byte order mark is ignored.
func parseMarkdown(data []byte) *markdownDoc {
	doc := &markdownDoc{}
	if d := checkText(data); d != nil {
		doc.problems = []diagnostic{*d}
		return doc
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 byte order mark
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))

	ls := strings.Split(string(data), "\n")
	if strings.TrimRight(ls[0], " \t") == "---" {
		end := -1
		for i := 1; i < len(ls); i++ {
			if strings.TrimRight(ls[i], " \t") == "---" {
				end = i
				break
			}
		}
		if end < 0 {
			doc.fmProblem = &diagnostic{Code: codeFrontmatter, Line: 1, Column: 1,
				Message: "the frontmatter that starts on line 1 has no closing --- line"}
		} else {
			doc.frontmatter = parseYAML([]byte(strings.Join(ls[1:end], "\n")), 1)
			// Blank the block so the body keeps its line numbers.
			for i := 0; i <= end; i++ {
				ls[i] = ""
			}
			data = []byte(strings.Join(ls, "\n"))
		}
	}
	doc.headings = markdown.Headings(data)
	return doc
}

// checkMarkdown checks one Markdown file's frontmatter and headings.
func (r *repo) checkMarkdown(m *markdownCheck, e *entry) []diagnostic {
	key := "markdown:" + e.path
	doc, cached := r.parsed[key].(*markdownDoc)
	if !cached {
		data, ok := r.readFile(e)
		if !ok {
			return nil
		}
		doc = parseMarkdown(data)
		r.parsed[key] = doc
	}
	var ds []diagnostic
	add := func(code string, line int, format string, args ...any) {
		ds = append(ds, diagnostic{Code: code, Path: e.path, Line: line, Message: fmt.Sprintf(format, args...)})
	}
	for _, p := range doc.problems {
		p.Path = e.path
		ds = append(ds, p)
	}
	if len(ds) > 0 {
		return ds
	}

	if f := m.Frontmatter; f != nil {
		switch {
		case doc.fmProblem != nil:
			p := *doc.fmProblem
			p.Path = e.path
			ds = append(ds, p)
		case doc.frontmatter == nil:
			if f.Required {
				add(codeFrontmatterMiss, 0, "has no frontmatter; start the file with a block between --- lines")
			}
		case len(doc.frontmatter.problems) > 0:
			for _, p := range doc.frontmatter.problems {
				p.Code, p.Path = codeFrontmatter, e.path
				p.Message = "frontmatter " + p.Message
				ds = append(ds, p)
			}
		default:
			if _, ok := doc.frontmatter.value.(map[string]any); !ok {
				at := doc.frontmatter.positions[""]
				add(codeFrontmatter, at.line, "frontmatter must be a mapping")
			} else if f.Schema != nil {
				ds = append(ds, validate(f.Schema.compiled, doc.frontmatter, e.path)...)
			}
		}
	}

	positions := map[string]int{} // heading text -> line, for headings that appear once
	for _, h := range m.Headings {
		var found []markdown.Heading
		for _, got := range doc.headings {
			if got.Level == h.Level && got.Text == h.Text {
				found = append(found, got)
			}
		}
		label := fmt.Sprintf("H%d %q", h.Level, h.Text)
		switch {
		case len(found) == h.Count:
		case len(found) == 0:
			add(codeHeadingMissing, 0, "required %s is absent", label)
		case h.Count == 0:
			add(codeHeadingCount, found[0].Line, "%s is not allowed", label)
		default:
			add(codeHeadingCount, found[min(h.Count, len(found)-1)].Line, "%s appears %d times, want %d", label, len(found), h.Count)
		}
		if h.Body == bodyNonempty {
			for _, got := range found {
				if !got.HasBody {
					add(codeSectionEmpty, got.Line, "the %s section is empty", label)
				}
			}
		}
		if len(found) == 1 {
			positions[h.Text] = found[0].Line
		}
	}
	for i := 1; i < len(m.Order); i++ {
		prev, cur := m.Order[i-1], m.Order[i]
		pl, okPrev := positions[prev]
		cl, okCur := positions[cur]
		if okPrev && okCur && cl < pl {
			add(codeHeadingOrder, cl, "%q must come after %q (line %d)", cur, prev, pl)
		}
	}
	return ds
}

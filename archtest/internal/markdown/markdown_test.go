package markdown

import (
	"reflect"
	"testing"
)

func TestHeadings(t *testing.T) {
	src := "# Title\n" + // 1
		"\n" +
		"## Purpose\n" + // 3
		"\n" +
		"Some *text*.\n" +
		"\n" +
		"## Usage   with  `code`\n" + // 7
		"\n" +
		"```\n" +
		"## not a heading\n" +
		"```\n" +
		"\n" +
		"Setext **status**\n" + // 13
		"-----------------\n" +
		"\n" +
		"<!-- only a comment -->\n" +
		"\n" +
		"### Child\n" + // 18
		"\n" +
		"child text\n" +
		"\n" +
		"## Empty\n" + // 22
		"\n" +
		"---\n" +
		"\n" +
		"- ## in a list\n"
	got := Headings([]byte(src))
	want := []Heading{
		{Level: 1, Text: "Title", Line: 1, HasBody: true},
		{Level: 2, Text: "Purpose", Line: 3, HasBody: true},
		{Level: 2, Text: "Usage with code", Line: 7, HasBody: true},
		{Level: 2, Text: "Setext status", Line: 13, HasBody: true}, // its child section has text
		{Level: 3, Text: "Child", Line: 18, HasBody: true},
		{Level: 2, Text: "Empty", Line: 22, HasBody: true}, // the list holds content
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Headings =\n%+v\nwant\n%+v", got, want)
	}
}

func TestEmptySections(t *testing.T) {
	src := "## A\n\n<!-- todo -->\n\n## B\n\n### B1\n\n## C\n\n```\n```\n"
	got := Headings([]byte(src))
	for _, h := range got {
		if h.HasBody {
			t.Errorf("%q has a body, want none", h.Text)
		}
	}
	if len(got) != 4 {
		t.Fatalf("got %d headings, want 4: %+v", len(got), got)
	}
}

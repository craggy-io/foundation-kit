package taxonomy_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/craggy-io/foundation-kit/taxonomy"
)

func base() taxonomy.Set {
	return taxonomy.Set{Owner: "host", Types: []taxonomy.Type{
		{Name: "bytes"},
		{Name: "tree"},
		{Name: "archive", Parents: []string{"bytes"}, Abstract: true},
		{Name: "archive.zip", Parents: []string{"archive"}},
		{Name: "archive.jar", Parents: []string{"archive.zip", " archive.zip ", "archive.jar"}},
	}}
}

func TestQueries(t *testing.T) {
	tx, err := taxonomy.New(base())
	if err != nil {
		t.Fatal(err)
	}
	if !tx.IsA("archive.jar", "bytes") || !tx.IsA("archive.jar", "archive.jar") || tx.IsA("archive.zip", "archive.jar") {
		t.Fatal("IsA wrong")
	}
	if tx.IsA("nope", "nope") || tx.IsA("archive.jar", "tree") {
		t.Fatal("IsA accepts an undeclared or unrelated type")
	}
	if got := tx.Ancestors("archive.jar"); !slices.Equal(got, []string{"archive", "archive.zip", "bytes"}) {
		t.Fatalf("Ancestors = %v", got)
	}
	if got := tx.Descendants("archive"); !slices.Equal(got, []string{"archive.jar", "archive.zip"}) {
		t.Fatalf("Descendants = %v", got)
	}
	if tx.Depth("archive.jar") != 3 || tx.Depth("tree") != 0 {
		t.Fatalf("Depth = %d, %d", tx.Depth("archive.jar"), tx.Depth("tree"))
	}
	if got := tx.Roots(); !slices.Equal(got, []string{"bytes", "tree"}) {
		t.Fatalf("Roots = %v", got)
	}
	jar, ok := tx.Get("archive.jar")
	if !ok || !slices.Equal(jar.Parents, []string{"archive.zip"}) {
		t.Fatalf("parents not normalised: %v", jar.Parents)
	}
	jar.Parents[0] = "changed"
	if again, _ := tx.Get("archive.jar"); again.Parents[0] != "archive.zip" {
		t.Fatal("Get returned shared state")
	}
	if got := tx.Closure(map[string]bool{"archive.zip": true}); len(got) != 3 || !got["bytes"] {
		t.Fatalf("Closure = %v", got)
	}
	if tx.Len() != 5 || len(tx.Types()) != 5 || !slices.Equal(tx.Owners("tree"), []string{"host"}) {
		t.Fatal("listing wrong")
	}
}

func TestRevisionTracksContent(t *testing.T) {
	a, _ := taxonomy.New(base())
	b, _ := taxonomy.New(taxonomy.Set{Owner: "someone-else", Types: base().Types})
	changed := base()
	changed.Types[3].Description = "ZIP archive"
	c, _ := taxonomy.New(changed)
	if a.Revision() != b.Revision() {
		t.Fatal("revision depends on the owner")
	}
	if a.Revision() == c.Revision() {
		t.Fatal("revision ignores a changed type")
	}
}

func TestSharedAndConflictingDeclarations(t *testing.T) {
	same := taxonomy.Set{Owner: "other", Types: []taxonomy.Type{{Name: "archive.zip", Parents: []string{"archive"}}}}
	tx, err := taxonomy.New(base(), same)
	if err != nil {
		t.Fatal(err)
	}
	if got := tx.Owners("archive.zip"); !slices.Equal(got, []string{"host", "other"}) {
		t.Fatalf("Owners = %v", got)
	}
	differs := taxonomy.Set{Owner: "other", Types: []taxonomy.Type{{Name: "archive.zip", Parents: []string{"bytes"}}}}
	wantCodes(t, taxonomy.CodeTypeConflict, base(), differs)
}

func TestEveryProblemIsReported(t *testing.T) {
	_, err := taxonomy.New(taxonomy.Set{Owner: "host", Types: []taxonomy.Type{
		{Name: "bytes"},
		{Name: "Upper.Case"},
		{Name: "a.b", Parents: []string{"nope"}},
		{Name: "c.x", Parents: []string{"c.y"}},
		{Name: "c.y", Parents: []string{"c.x"}},
		{Name: "d", Parents: []string{"bytes"}, Abstract: true},
		{Name: "d.x", Parents: []string{"bytes"}},
	}}, taxonomy.Set{Owner: ""})
	var e *taxonomy.Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %v", err)
	}
	for _, code := range []taxonomy.Code{
		taxonomy.CodeNameInvalid, taxonomy.CodeParentUnknown, taxonomy.CodeCycle,
		taxonomy.CodeNamespaceEscape, taxonomy.CodeOwnerInvalid,
	} {
		if !e.Has(code) {
			t.Errorf("missing %s in %v", code, err)
		}
	}
}

func wantCodes(t *testing.T, code taxonomy.Code, sets ...taxonomy.Set) {
	t.Helper()
	_, err := taxonomy.New(sets...)
	var e *taxonomy.Error
	if !errors.As(err, &e) || !e.Has(code) {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestNames(t *testing.T) {
	for name, want := range map[string]bool{
		"bytes": true, "archive.zip": true, "source-tree.java": true, "a.b+c_d": true,
		"": false, ".a": false, "a.": false, "a..b": false, "Upper": false, "a b": false,
	} {
		if taxonomy.ValidName(name) != want {
			t.Errorf("ValidName(%q) = %v", name, !want)
		}
	}
	if taxonomy.Namespace("archive.zip.x") != "archive" || taxonomy.Namespace("bytes") != "" {
		t.Fatal("Namespace wrong")
	}
}

func TestLive(t *testing.T) {
	l := taxonomy.NewLive()
	if l.Taxonomy().Len() != 0 {
		t.Fatal("a new Live isn't empty")
	}
	if _, err := l.Declare("host", []taxonomy.Type{{Name: "tree"}, {Name: "source-tree", Parents: []string{"tree"}}}); err != nil {
		t.Fatal(err)
	}
	before := l.Taxonomy()

	java := []taxonomy.Type{{Name: "source-tree.java", Parents: []string{"source-tree"}}}
	preview, err := l.Preview("java", java)
	if err != nil || !preview.IsA("source-tree.java", "tree") {
		t.Fatalf("Preview: %v", err)
	}
	if l.Taxonomy() != before {
		t.Fatal("Preview published")
	}
	if _, err := l.Declare("java", java); err != nil {
		t.Fatal(err)
	}
	if !l.Taxonomy().IsA("source-tree.java", "tree") || before.Has("source-tree.java") {
		t.Fatal("Declare didn't publish a new taxonomy, or changed the old one")
	}

	// A failed change leaves the published taxonomy alone.
	published := l.Taxonomy()
	if _, err := l.Declare("bad", []taxonomy.Type{{Name: "x.y", Parents: []string{"missing"}}}); err == nil {
		t.Fatal("Declare accepted an unknown parent")
	}
	var e *taxonomy.Error
	if _, err := l.Withdraw("host"); !errors.As(err, &e) || !e.Has(taxonomy.CodeOwnerRequired) {
		t.Fatalf("Withdraw of a needed owner: %v", err)
	}
	if l.Taxonomy() != published || !slices.Equal(l.Owners(), []string{"host", "java"}) {
		t.Fatal("a failed change altered the Live")
	}

	// Redeclaring replaces the owner's whole set.
	if _, err := l.Declare("java", []taxonomy.Type{{Name: "source-tree.kotlin", Parents: []string{"source-tree"}}}); err != nil {
		t.Fatal(err)
	}
	if l.Taxonomy().Has("source-tree.java") || !l.Taxonomy().Has("source-tree.kotlin") {
		t.Fatal("Declare didn't replace the owner's set")
	}
	if _, err := l.Withdraw("java"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Withdraw("java"); err != nil {
		t.Fatal("withdrawing twice failed")
	}
	if l.Taxonomy().Len() != 2 {
		t.Fatalf("Len = %d", l.Taxonomy().Len())
	}
}

func TestLiveConcurrentUse(t *testing.T) {
	l := taxonomy.NewLive()
	if _, err := l.Declare("host", []taxonomy.Type{{Name: "tree"}}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			owner := string(rune('a' + i))
			for range 50 {
				if _, err := l.Declare(owner, []taxonomy.Type{{Name: "tree." + owner, Parents: []string{"tree"}}}); err != nil {
					t.Error(err)
				}
				_ = l.Taxonomy().IsA("tree."+owner, "tree")
			}
		}()
	}
	wg.Wait()
	if l.Taxonomy().Len() != 9 {
		t.Fatalf("Len = %d", l.Taxonomy().Len())
	}
}

package kind

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseID(t *testing.T) {
	valid := []struct {
		in   string
		want ID
	}{
		{"cclab.intel-pack@1", ID{Name: "cclab.intel-pack", Version: 1}},
		{"craggy.agents.team@12", ID{Name: "craggy.agents.team", Version: 12}},
	}
	for _, tt := range valid {
		got, err := ParseID(tt.in)
		if err != nil {
			t.Errorf("ParseID(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseID(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
		if got.String() != tt.in {
			t.Errorf("ParseID(%q).String() = %q, want the input unchanged", tt.in, got.String())
		}
	}

	invalid := []string{
		"",
		"example.script",                      // no version
		"example.script@",                     // empty version
		"example.script/v1",                   // the old form
		"example.script@v1",                   // v prefix
		"example.script@0",                    // versions start at 1
		"example.script@01",                   // leading zero
		"example.script@-1",                   // negative
		"example.script@+1",                   // sign
		"example.script@1.2",                  // not a whole number
		"example.script@1@2",                  // two versions
		"example.script@99999999999999999999", // out of range
		"script@1",                            // no namespace
		"@1",                                  // no name
		"Example.Script@1",                    // upper case
		"example..script@1",                   // empty segment
		".example.script@1",                   // leading dot
		"example.script.@1",                   // trailing dot
		"example.-script@1",                   // leading hyphen
		"example.script-@1",                   // trailing hyphen
		"example.my_script@1",                 // underscore
		"example.script @1",                   // space
	}
	for _, in := range invalid {
		if got, err := ParseID(in); err == nil {
			t.Errorf("ParseID(%q) = %+v, want an error", in, got)
		}
	}
}

func TestIDValidate(t *testing.T) {
	if err := (ID{Name: "example.script", Version: 1}).Validate(); err != nil {
		t.Errorf("valid ID: %v", err)
	}
	for _, id := range []ID{
		{},
		{Name: "example.script"},
		{Name: "example.script", Version: -1},
		{Name: "script", Version: 1},
	} {
		if err := id.Validate(); err == nil {
			t.Errorf("ID%+v.Validate() = nil, want an error", id)
		}
	}
}

// schemaFunc adapts a function to the Schema interface.
type schemaFunc func(any) error

func (f schemaFunc) Validate(v any) error { return f(v) }

type profile struct {
	Name    string `json:"name"`
	Retries int    `json:"retries"`
}

func TestValidateWithoutSchemaChecksOnlyTheGoType(t *testing.T) {
	k := New[profile]("example.profile", 1)
	if k.Schema() != nil {
		t.Error("Schema() is not nil for a kind created without one")
	}
	if err := k.Validate(profile{}); err != nil {
		t.Errorf("Validate(profile): %v", err)
	}
	if err := k.Validate("not a profile"); err == nil {
		t.Error("Validate(string) = nil, want an error for the wrong Go type")
	}
}

func TestValidateGivesTheSchemaTheJSONForm(t *testing.T) {
	var got any
	k := New[profile]("example.profile", 1, WithSchema(schemaFunc(func(v any) error {
		got = v
		return nil
	})))
	if err := k.Validate(profile{Name: "default", Retries: 3}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	doc, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("schema received %T, want map[string]any", got)
	}
	if doc["name"] != "default" {
		t.Errorf(`doc["name"] = %v, want "default"`, doc["name"])
	}
	if doc["retries"] != json.Number("3") {
		t.Errorf(`doc["retries"] = %#v, want json.Number("3")`, doc["retries"])
	}
}

func TestValidateReturnsSchemaErrors(t *testing.T) {
	want := errors.New("bad value")
	k := New[profile]("example.profile", 1, WithSchema(schemaFunc(func(any) error { return want })))
	if err := k.Validate(profile{}); !errors.Is(err, want) {
		t.Errorf("Validate = %v, want it to wrap %v", err, want)
	}
}

func TestBuiltin(t *testing.T) {
	k := New[profile]("example.profile", 1)
	value := profile{Name: "default"}
	b := Builtin(k, "default", value)
	if b.Kind().ID() != k.ID() {
		t.Errorf("Kind().ID() = %v, want %v", b.Kind().ID(), k.ID())
	}
	if b.ID() != "default" {
		t.Errorf("ID() = %q, want default", b.ID())
	}
	if b.Value() != value {
		t.Errorf("Value() = %v, want %v", b.Value(), value)
	}
	if err := b.Kind().Validate(b.Value()); err != nil {
		t.Errorf("a built-in's value should pass its own kind's check: %v", err)
	}
}

func TestGlobal(t *testing.T) {
	if New[string]("a.b", 1).Global() {
		t.Error("a kind is global without the Global option")
	}
	var k AnyKind = New[string]("a.b", 1, Global(), WithSchema(nil))
	if !k.Global() {
		t.Error("Global() = false for a kind declared with Global")
	}
}

package canonjson_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/craggy-io/foundation-kit/canonjson"
)

// These outputs are stored as digests by consumers. Changing one is a format break.
var golden = []struct{ in, want string }{
	{`{"z":1.00,"a":[1e2,-0]}`, `{"a":[100,0],"z":1}`},
	{`  {"k" : [ 1 , 2 ] }  `, `{"k":[1,2]}`},
	{`[1.50,2.500e3,-3.25e-2,1E+2,10e-1]`, `[1.5,2500,-0.0325,100,1]`},
	{`1e-7`, `0.0000001`},
	{`123456789012345678901234567890`, `123456789012345678901234567890`},
	{`9007199254740993`, `9007199254740993`},
	{`1.0000000000000000000001`, `1.0000000000000000000001`},
	{`{"<a>":"&","b":"\u2028"}`, `{"\u003ca\u003e":"\u0026","b":"\u2028"}`},
	{`{"é":1,"z":2,"Z":3}`, `{"Z":3,"z":2,"é":1}`},
	{`"\ud83d\ude00"`, `"😀"`},
	{`[]`, `[]`},
	{`{}`, `{}`},
	{`null`, `null`},
}

func TestGolden(t *testing.T) {
	for _, g := range golden {
		got, err := canonjson.Normalize([]byte(g.in))
		if err != nil || string(got) != g.want {
			t.Errorf("Normalize(%s) = %s, %v; want %s", g.in, got, err, g.want)
			continue
		}
		again, err := canonjson.Normalize(got)
		if err != nil || !bytes.Equal(again, got) {
			t.Errorf("Normalize isn't idempotent on %s: %s, %v", got, again, err)
		}
	}
}

func TestRejects(t *testing.T) {
	tests := []struct {
		in   string
		want error
	}{
		{`{"a":1,"a":2}`, canonjson.ErrInvalid},
		{`{} {}`, canonjson.ErrInvalid},
		{`[1,]`, canonjson.ErrInvalid},
		{`[1`, canonjson.ErrInvalid},
		{``, canonjson.ErrInvalid},
		{"\"\xff\"", canonjson.ErrInvalid},
		{`1e999999999`, canonjson.ErrLimit},
		{strings.Repeat("[", 70) + strings.Repeat("]", 70), canonjson.ErrLimit},
		{"[" + strings.Repeat("1e4096,", 2100) + "0]", canonjson.ErrLimit},
	}
	for _, tt := range tests {
		_, err := canonjson.Normalize([]byte(tt.in))
		if !errors.Is(err, tt.want) {
			t.Errorf("Normalize(%.40q) err = %v, want %v", tt.in, err, tt.want)
		}
	}
}

func TestLargeExponentsStayClosed(t *testing.T) {
	for _, raw := range []string{"1e1024", "-1e4096", "1e-4096"} {
		a, err := canonjson.Normalize([]byte(raw))
		if err != nil {
			t.Fatal(raw, err)
		}
		b, err := canonjson.Normalize(a)
		if err != nil || !bytes.Equal(a, b) {
			t.Fatalf("not idempotent: %s: %v", raw, err)
		}
	}
}

func TestMarshal(t *testing.T) {
	type rec struct {
		B float64 `json:"b"`
		A string  `json:"a"`
	}
	got, err := canonjson.Marshal(rec{B: 2.0, A: "x"})
	if err != nil || string(got) != `{"a":"x","b":2}` {
		t.Fatalf("Marshal = %s, %v", got, err)
	}
}

func ExampleNormalize() {
	a, _ := canonjson.Normalize([]byte(`{"b": 1.0, "a": [1e2]}`))
	b, _ := canonjson.Normalize([]byte(`{"a":[100],"b":1}`))
	fmt.Println(string(a), bytes.Equal(a, b))
	// Output: {"a":[100],"b":1} true
}

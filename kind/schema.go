package kind

// Schema validates the data of a definition. It is an optional check on top of the
// Go type: it suits data-shaped kinds, and a Starlark or Markdown kind would usually
// have none.
//
// Validate receives the JSON form of the value: map[string]any, []any, string,
// json.Number, bool or nil. See package jsonschema for a JSON Schema implementation.
//
// A schema and a Go type can drift apart, so test them together with a sample value
// using bundletest.CheckSample.
type Schema interface {
	Validate(value any) error
}

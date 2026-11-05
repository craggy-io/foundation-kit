package kind

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// AnyKind is a Kind of any value type. It is implemented only by *Kind[T], which
// keeps type erasure at this one boundary.
type AnyKind interface {
	// ID returns the kind's name and version.
	ID() ID

	// Schema returns the kind's schema, or nil if it has none.
	Schema() Schema

	// Global reports whether the kind's contribution IDs are unique across the
	// whole catalog. See Global.
	Global() bool

	// Validate checks that value has the kind's Go type and satisfies its schema,
	// if it has one.
	Validate(value any) error

	sealed()
}

// Kind is a versioned type of definition whose values have the Go type T. Binding
// the name, version and T in one value means callers cannot read a kind as the wrong
// type, and never need to cast.
type Kind[T any] struct {
	id     ID
	schema Schema
	global bool
}

// compile-time check that *Kind[T] satisfies AnyKind.
var _ AnyKind = (*Kind[any])(nil)

// Option configures a Kind.
type Option func(*settings)

type settings struct {
	schema Schema
	global bool
}

// WithSchema attaches a schema to a kind.
func WithSchema(s Schema) Option {
	return func(o *settings) { o.schema = s }
}

// Global marks a kind whose definitions are looked up by contribution ID alone,
// across every bundle and provider, such as agent or profile names. bundle.Load then
// rejects two effective definitions of the kind that share a contribution ID, and
// bundle.Lookup finds one by that ID. Definitions of other kinds are only unique by
// their full Ref.
func Global() Option {
	return func(o *settings) { o.global = true }
}

// New creates a kind, normally once as a package-level variable in the library or
// application that owns it. It does not register anything globally.
//
// New never fails, so that kinds can be package-level variables. Whoever uses the
// kind checks it with ID().Validate, which bundle.Load does for every kind it receives.
func New[T any](name string, version int, opts ...Option) *Kind[T] {
	var o settings
	for _, opt := range opts {
		opt(&o)
	}
	return &Kind[T]{id: ID{Name: name, Version: version}, schema: o.schema, global: o.global}
}

// ID returns the kind's name and version.
func (k *Kind[T]) ID() ID { return k.id }

// Schema returns the kind's schema, or nil if it has none.
func (k *Kind[T]) Schema() Schema { return k.schema }

// Global reports whether the kind was declared with Global, so that its contribution
// IDs are unique across the whole catalog.
func (k *Kind[T]) Global() bool { return k.global }

func (k *Kind[T]) sealed() {}

// Validate checks that value has the Go type T and satisfies the kind's schema, if
// it has one. The schema receives value's JSON form, as described on Schema.
func (k *Kind[T]) Validate(value any) error {
	if _, ok := value.(T); !ok {
		return fmt.Errorf("kind: %s: value has type %T, which is not this kind's Go type", k.id, value)
	}
	if k.schema == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("kind: %s: encoding value for schema validation: %w", k.id, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("kind: %s: decoding value for schema validation: %w", k.id, err)
	}
	if err := k.schema.Validate(doc); err != nil {
		return fmt.Errorf("kind: %s: %w", k.id, err)
	}
	return nil
}

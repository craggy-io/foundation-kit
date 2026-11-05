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
}

// compile-time check that *Kind[T] satisfies AnyKind.
var _ AnyKind = (*Kind[any])(nil)

// Option configures a Kind.
type Option func(*settings)

type settings struct {
	schema Schema
}

// WithSchema attaches a schema to a kind.
func WithSchema(s Schema) Option {
	return func(o *settings) { o.schema = s }
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
	return &Kind[T]{id: ID{Name: name, Version: version}, schema: o.schema}
}

// ID returns the kind's name and version.
func (k *Kind[T]) ID() ID { return k.id }

// Schema returns the kind's schema, or nil if it has none.
func (k *Kind[T]) Schema() Schema { return k.schema }

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

package kind

// Provider is Go code that supplies built-in definitions. A library can export a
// Provider without importing the bundle loader, and the host decides whether to use it.
type Provider struct {
	// ID is the provider's identity. It shares a namespace with bundle IDs, because
	// both are the owner part of a Ref.
	ID string

	// Version is the provider's own release label. A built-in has no file bytes to
	// hash, so this is the only thing that identifies its content.
	Version string

	// Builtins lists the definitions this provider supplies.
	Builtins []AnyBuiltin
}

// AnyBuiltin is a built-in definition of any value type. It is created by Builtin.
type AnyBuiltin interface {
	// Kind returns the built-in's kind.
	Kind() AnyKind

	// ID returns the built-in's ID, unique within its Provider.
	ID() string

	// Value returns the definition's value, which has the kind's Go type.
	Value() any

	sealed()
}

// Builtin creates a definition supplied by Go code rather than by a bundle. It needs
// no compiler, and the type system guarantees that value matches the kind.
func Builtin[T any](k *Kind[T], id string, value T) AnyBuiltin {
	return builtin{kind: k, id: id, value: value}
}

type builtin struct {
	kind  AnyKind
	id    string
	value any
}

func (b builtin) Kind() AnyKind { return b.kind }
func (b builtin) ID() string    { return b.id }
func (b builtin) Value() any    { return b.value }
func (b builtin) sealed()       {}

// Package settings holds live, user-editable values, declared by the code that reads
// them.
//
// Code declares a setting next to where it is used, with Int, Bool, Choice or String,
// and keeps the returned Setting. One Store holds every declaration, validates changes
// against it, and serves the list that a settings UI is generated from. The Store knows
// nothing about what a setting bounds: the key's first segments name its owner, and the
// declaration carries everything else.
//
// A setting isn't a definition. Definitions are authored and shipped in bundles, while
// settings are tuned by a person at run time.
//
// # Invariants
//
//   - Only changed values are stored. A setting nobody changed follows its declaration.
//   - A setting's value comes from the first of these that exists: a stored value that
//     still fits the declaration, the environment, then the declared default.
//   - A stored value that no longer fits is reported in Info.StoredError and not
//     applied. It stays in storage until its own key is set or reset.
//   - A stored value for a key nothing has declared is kept, so that turning a feature
//     off doesn't erase what the user chose for it.
//   - Damaged storage fails New, so it never silently discards what the user set.
//   - A secret's value is never served by Get or List, only whether it has one. It is
//     still written to storage in plain text.
//
// # Concurrency
//
// A Store and a Setting are safe for concurrent use. Setting.Get never blocks and sees
// a change as soon as Set returns, so a caller should read it once per operation and
// keep that value for the whole operation. A Store assumes it is the only writer to its
// Storage, and doesn't notice changes made by anything else.
//
// A malformed declaration panics, because it's a programming error that the first run
// finds. Values from a user or from storage never panic.
//
// # Exported names
//
//	Store, New                     hold declarations and stored values
//	Storage, File                  where stored values are kept
//	Setting                        a declared setting, read with Get
//	Int, Bool, Choice, String      declare a setting
//	Meta                           what every declaration carries
//	IntSpec, BoolSpec              declarations of each type
//	ChoiceSpec, ChoiceOption
//	StringSpec
//	Info                           one setting as a UI sees it
//	Type, Unit, Origin             what Info reports about a setting
//	ErrNotFound, ErrInvalid        errors to match with errors.Is
package settings

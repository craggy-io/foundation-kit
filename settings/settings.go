package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// ErrNotFound reports a key nothing has declared.
var ErrNotFound = errors.New("settings: no such setting")

// ErrInvalid reports a value that a setting's declaration rejects.
var ErrInvalid = errors.New("settings: invalid value")

// Type is a setting's value type, which picks a UI's control.
type Type string

const (
	TypeInteger Type = "integer"
	TypeBoolean Type = "boolean"
	TypeChoice  Type = "choice"
	TypeString  Type = "string"
)

// Origin says where a setting's current value comes from.
type Origin string

const (
	OriginStored      Origin = "stored"
	OriginEnvironment Origin = "environment"
	OriginDefault     Origin = "default"
)

// Unit says how an integer setting's value reads, which picks its formatting and input
// in a UI, so that a byte size can read as 1 GiB.
type Unit string

const (
	UnitNone  Unit = ""
	UnitCount Unit = "count"
	UnitBytes Unit = "bytes"
)

// maxStringBytes bounds a string setting's value.
const maxStringBytes = 4096

// keyPattern is at least two dotted segments, so that a key always names its owner.
var keyPattern = regexp.MustCompile(`^[a-z0-9_-]+(\.[a-z0-9_-]+)+$`)

// Meta is what every declaration carries for its owner and for a UI.
type Meta struct {
	// Key is dotted and lower-case, and its first segments name the owner, for example
	// "search.strings.max-bytes".
	Key string

	// Title is a short, sentence-case label. It is required.
	Title string

	// Description says what the setting bounds and what happens at the bound, for the
	// person or agent about to change it.
	Description string

	// Group is the section of a settings panel. It is required.
	Group string

	// Advanced settings start collapsed in a UI.
	Advanced bool

	// Restart tells a UI that a change applies at the next start, because the owner
	// reads the setting only once at startup. It is advice: the Store doesn't enforce
	// it, and Setting.Get returns a new value as soon as it is set.
	Restart bool
}

// ChoiceOption is one allowed value of a choice setting.
type ChoiceOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// IntSpec declares an integer setting bounded to [Min, Max].
type IntSpec[T ~int | ~int64] struct {
	Meta
	Default, Min, Max T
	Unit              Unit
}

// BoolSpec declares an on/off setting.
type BoolSpec struct {
	Meta
	Default bool
}

// ChoiceSpec declares a setting that takes one of the values in Choices.
type ChoiceSpec struct {
	Meta
	Default string
	Choices []ChoiceOption
}

// StringSpec declares a one-line text setting, such as a path, URL or credential.
type StringSpec struct {
	Meta
	Default string

	// Env names environment variables to use, in order, when nothing is stored. The
	// environment is read once, when the setting is declared.
	Env []string

	// Secret values are never served: Info.HasValue says whether there is one.
	Secret bool
}

// Info is one setting as a UI sees it: its declaration plus its current state. A UI can
// render a setting from Info alone. Value and Default hold the JSON form of the value,
// the same form Set accepts, and are empty for a secret.
type Info struct {
	Key         string          `json:"key"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Group       string          `json:"group"`
	Advanced    bool            `json:"advanced,omitempty"`
	Restart     bool            `json:"restart,omitempty"`
	Type        Type            `json:"type"`
	Unit        Unit            `json:"unit,omitempty"`
	Min         *int64          `json:"min,omitempty"`
	Max         *int64          `json:"max,omitempty"`
	Choices     []ChoiceOption  `json:"choices,omitempty"`
	Default     json.RawMessage `json:"default"`
	Value       json.RawMessage `json:"value"`

	// Stored says whether the current value comes from storage.
	Stored bool `json:"stored"`

	// Env lists the environment variables a string setting falls back to, and EnvVar
	// is the first of them that the host sets.
	Env    []string `json:"env,omitempty"`
	EnvVar string   `json:"envVar,omitempty"`

	Origin   Origin `json:"origin"`
	Secret   bool   `json:"secret,omitempty"`
	HasValue bool   `json:"hasValue,omitempty"`

	// StoredError says why a stored value wasn't applied, because it no longer fits the
	// declaration. The setting uses its fallback until the user sets or resets it.
	StoredError string `json:"storedError,omitempty"`
}

// Setting is a declared setting. Get is safe for concurrent use. The zero value isn't
// usable: get a Setting from Int, Bool, Choice or String.
type Setting[T any] struct{ d *declaration }

// Get returns the setting's current value.
func (s Setting[T]) Get() T { return (*s.d.value.Load()).(T) }

// declaration is one setting in a Store.
type declaration struct {
	info     Info // the static part; infoLocked fills in the rest on each read
	fallback any  // the value when nothing usable is stored
	parse    func(json.RawMessage) (any, error)
	value    atomic.Pointer[any]
}

// Store holds the declared settings and the stored values. It is safe for concurrent
// use.
type Store struct {
	storage Storage

	mu           sync.Mutex
	declarations map[string]*declaration
	stored       map[string]json.RawMessage
	rejected     map[string]string // key -> why its stored value wasn't applied
}

// New reads the stored values from storage. Finding nothing stored gives an empty
// Store, while stored data that isn't a JSON object is an error, so that damage never
// silently discards what the user set.
func New(ctx context.Context, storage Storage) (*Store, error) {
	if storage == nil {
		return nil, errors.New("settings: storage is nil")
	}
	s := &Store{
		storage:      storage,
		declarations: map[string]*declaration{},
		stored:       map[string]json.RawMessage{},
		rejected:     map[string]string{},
	}
	data, err := storage.Read(ctx)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("settings: reading stored values: %w", err)
	}
	if err := json.Unmarshal(data, &s.stored); err != nil {
		return nil, fmt.Errorf("settings: stored values aren't a JSON object: %w", err)
	}
	if s.stored == nil {
		s.stored = map[string]json.RawMessage{}
	}
	return s, nil
}

// Int declares an integer setting. It panics on a malformed declaration or a key
// declared twice, because both are programming errors.
func Int[T ~int | ~int64](s *Store, spec IntSpec[T]) Setting[T] {
	if spec.Min > spec.Max || spec.Default < spec.Min || spec.Default > spec.Max {
		panic(fmt.Sprintf("settings: %s: default %d is outside [%d, %d]", spec.Key, spec.Default, spec.Min, spec.Max))
	}
	lo, hi := int64(spec.Min), int64(spec.Max)
	info := spec.info(TypeInteger)
	info.Unit, info.Min, info.Max = spec.Unit, &lo, &hi
	info.Default = mustJSON(spec.Default)
	return declare(s, info, spec.Default, func(raw json.RawMessage) (any, error) {
		var n int64
		if err := decode(raw, &n); err != nil {
			return nil, invalidf("%s takes a whole number", spec.Key)
		}
		if n < lo || n > hi {
			return nil, invalidf("%s takes %d to %d, got %d", spec.Key, lo, hi, n)
		}
		return T(n), nil
	})
}

// Bool declares an on/off setting. It panics like Int.
func Bool(s *Store, spec BoolSpec) Setting[bool] {
	info := spec.info(TypeBoolean)
	info.Default = mustJSON(spec.Default)
	return declare(s, info, spec.Default, func(raw json.RawMessage) (any, error) {
		var b bool
		if err := decode(raw, &b); err != nil {
			return nil, invalidf("%s takes true or false", spec.Key)
		}
		return b, nil
	})
}

// Choice declares a setting that takes one of the declared choices. It panics like Int,
// and on a default that isn't one of the choices.
func Choice(s *Store, spec ChoiceSpec) Setting[string] {
	allowed := make([]string, len(spec.Choices))
	for i, c := range spec.Choices {
		allowed[i] = c.Value
	}
	if !slices.Contains(allowed, spec.Default) {
		panic(fmt.Sprintf("settings: %s: default %q is not one of %v", spec.Key, spec.Default, allowed))
	}
	info := spec.info(TypeChoice)
	info.Choices = slices.Clone(spec.Choices)
	info.Default = mustJSON(spec.Default)
	return declare(s, info, spec.Default, func(raw json.RawMessage) (any, error) {
		var v string
		if err := decode(raw, &v); err != nil || !slices.Contains(allowed, v) {
			return nil, invalidf("%s takes one of %s", spec.Key, strings.Join(allowed, ", "))
		}
		return v, nil
	})
}

// String declares a text setting. It panics like Int. The host's environment is read
// once, here. An error from a string setting never includes the value, because the
// value may be a secret.
func String(s *Store, spec StringSpec) Setting[string] {
	info := spec.info(TypeString)
	info.Default = mustJSON(spec.Default)
	info.Secret, info.Env = spec.Secret, slices.Clone(spec.Env)
	fallback := spec.Default
	for _, name := range spec.Env {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			fallback, info.EnvVar = v, name
			break
		}
	}
	return declare(s, info, fallback, func(raw json.RawMessage) (any, error) {
		var v string
		if err := decode(raw, &v); err != nil {
			return nil, invalidf("%s takes text", spec.Key)
		}
		v = strings.TrimSpace(v)
		if strings.ContainsAny(v, "\r\n\x00") {
			return nil, invalidf("%s takes one line of text", spec.Key)
		}
		if len(v) > maxStringBytes {
			return nil, invalidf("%s takes at most %d bytes", spec.Key, maxStringBytes)
		}
		return v, nil
	})
}

func (m Meta) info(t Type) Info {
	if !keyPattern.MatchString(m.Key) {
		panic(fmt.Sprintf("settings: key %q must be lower-case with at least two dotted segments", m.Key))
	}
	if m.Title == "" || m.Group == "" {
		panic(fmt.Sprintf("settings: %s needs a title and a group", m.Key))
	}
	return Info{Key: m.Key, Title: m.Title, Description: m.Description, Group: m.Group, Advanced: m.Advanced, Restart: m.Restart, Type: t}
}

// declare adds one setting and applies its stored value if that still fits the
// declaration. Otherwise the setting starts on its fallback.
func declare[T any](s *Store, info Info, fallback T, parse func(json.RawMessage) (any, error)) Setting[T] {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.declarations[info.Key]; dup {
		panic("settings: " + info.Key + " declared twice")
	}
	d := &declaration{info: info, fallback: fallback, parse: parse}
	value := any(fallback)
	if raw, ok := s.stored[info.Key]; ok {
		if v, err := parse(raw); err != nil {
			shown := " " + string(raw)
			if info.Secret {
				shown = ""
			}
			s.rejected[info.Key] = fmt.Sprintf("stored value%s no longer fits: %v", shown, err)
		} else {
			value = v
		}
	}
	d.value.Store(&value)
	s.declarations[info.Key] = d
	return Setting[T]{d: d}
}

// List returns every declared setting, sorted by group and then key.
func (s *Store) List() []Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Info, 0, len(s.declarations))
	for key := range s.declarations {
		out = append(out, s.infoLocked(key))
	}
	slices.SortFunc(out, func(a, b Info) int {
		if c := strings.Compare(a.Group, b.Group); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
	return out
}

// Get returns one setting, or an error matching ErrNotFound.
func (s *Store) Get(key string) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.declarations[key]; !ok {
		return Info{}, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return s.infoLocked(key), nil
}

// Set validates a JSON value against the setting's declaration, stores it and makes it
// live. It returns an error matching ErrInvalid or ErrNotFound if the change is
// rejected. If storage fails, nothing changes.
func (s *Store) Set(ctx context.Context, key string, value json.RawMessage) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.declarations[key]
	if !ok {
		return Info{}, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	v, err := d.parse(value)
	if err != nil {
		return Info{}, err
	}
	if err := s.writeLocked(ctx, key, mustJSON(v)); err != nil {
		return Info{}, err
	}
	d.value.Store(&v)
	return s.infoLocked(key), nil
}

// Reset removes a setting's stored value, returning it to its fallback. If storage
// fails, nothing changes.
func (s *Store) Reset(ctx context.Context, key string) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.declarations[key]
	if !ok {
		return Info{}, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	if err := s.writeLocked(ctx, key, nil); err != nil {
		return Info{}, err
	}
	value := d.fallback
	d.value.Store(&value)
	return s.infoLocked(key), nil
}

func (s *Store) infoLocked(key string) Info {
	d := s.declarations[key]
	info := d.info
	// Info is handed to callers, so it must not share anything with the declaration.
	info.Choices, info.Env = slices.Clone(info.Choices), slices.Clone(info.Env)
	info.Default = slices.Clone(info.Default)
	if info.Min != nil {
		lo, hi := *info.Min, *info.Max
		info.Min, info.Max = &lo, &hi
	}
	value := *d.value.Load()
	info.Value = mustJSON(value)
	_, info.Stored = s.stored[key]
	if why, rejected := s.rejected[key]; rejected {
		info.Stored, info.StoredError = false, why
	}
	switch {
	case info.Stored:
		info.Origin = OriginStored
	case info.EnvVar != "":
		info.Origin = OriginEnvironment
	default:
		info.Origin = OriginDefault
	}
	if info.Secret {
		info.HasValue = value != ""
		info.Value, info.Default = nil, nil
	}
	return info
}

// writeLocked stores the values with key set to value, where nil removes it, and only
// then updates memory. Every other stored value is kept as it was, including those that
// were rejected or that nothing has declared.
func (s *Store) writeLocked(ctx context.Context, key string, value json.RawMessage) error {
	next := maps.Clone(s.stored)
	if value == nil {
		delete(next, key)
	} else {
		next[key] = value
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("settings: encoding stored values: %w", err)
	}
	if err := s.storage.Write(ctx, append(data, '\n')); err != nil {
		return fmt.Errorf("settings: writing stored values: %w", err)
	}
	s.stored = next
	delete(s.rejected, key)
	return nil
}

// decode reads a JSON value into v. It rejects null, which json.Unmarshal would
// otherwise accept as a zero value.
func decode(raw json.RawMessage, v any) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("null")
	}
	return json.Unmarshal(raw, v)
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// mustJSON encodes a setting's value, which is always an integer, bool or string, and
// so can't fail.
func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("settings: encoding %T: %v", v, err))
	}
	return data
}

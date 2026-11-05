package settings_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/craggy-io/foundation-kit/settings"
)

var ctx = context.Background()

func meta(key string) settings.Meta {
	return settings.Meta{Key: key, Title: "Title of " + key, Description: "Bounds " + key, Group: "Test"}
}

func open(t *testing.T, path string) *settings.Store {
	t.Helper()
	s, err := settings.New(ctx, settings.File(path))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "settings.json")
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// memory is a Storage that can be made to fail.
type memory struct {
	data              []byte
	readErr, writeErr error
	lastCtx           context.Context
}

func (m *memory) Read(ctx context.Context) ([]byte, error) {
	m.lastCtx = ctx
	if m.readErr != nil {
		return nil, m.readErr
	}
	if m.data == nil {
		return nil, fs.ErrNotExist
	}
	return m.data, nil
}

func (m *memory) Write(ctx context.Context, data []byte) error {
	m.lastCtx = ctx
	if m.writeErr != nil {
		return m.writeErr
	}
	m.data = data
	return nil
}

// A declared setting reads its default, takes validated changes live, survives a
// restart, and resets to its default.
func TestSetPersistsAndResets(t *testing.T) {
	path := tempPath(t)
	s := open(t, path)
	spec := settings.IntSpec[int64]{Meta: meta("a.max-bytes"), Default: 10, Min: 1, Max: 100, Unit: settings.UnitBytes}
	size := settings.Int(s, spec)
	if size.Get() != 10 {
		t.Fatalf("default = %d, want 10", size.Get())
	}
	info, err := s.Set(ctx, "a.max-bytes", raw(`42`))
	if err != nil || size.Get() != 42 || !info.Stored || string(info.Value) != "42" || info.Origin != settings.OriginStored {
		t.Fatalf("Set = %+v, %v; Get = %d", info, err, size.Get())
	}
	for _, bad := range []string{`0`, `101`, `"42"`, `4.5`, `true`, `null`, ` null `, ``, `1 2`, `99999999999999999999`} {
		if _, err := s.Set(ctx, "a.max-bytes", raw(bad)); !errors.Is(err, settings.ErrInvalid) {
			t.Errorf("Set(%q) = %v, want ErrInvalid", bad, err)
		}
	}
	if size.Get() != 42 {
		t.Fatalf("rejected values changed the setting to %d", size.Get())
	}
	if _, err := s.Set(ctx, "no.such", raw(`1`)); !errors.Is(err, settings.ErrNotFound) {
		t.Fatalf("Set(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := s.Reset(ctx, "no.such"); !errors.Is(err, settings.ErrNotFound) {
		t.Fatalf("Reset(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := s.Get("no.such"); !errors.Is(err, settings.ErrNotFound) {
		t.Fatalf("Get(unknown) = %v, want ErrNotFound", err)
	}

	reopened := open(t, path)
	again := settings.Int(reopened, spec)
	if again.Get() != 42 {
		t.Fatalf("value after restart = %d, want the stored 42", again.Get())
	}
	info, err = reopened.Reset(ctx, "a.max-bytes")
	if err != nil || again.Get() != 10 || info.Stored || info.Origin != settings.OriginDefault {
		t.Fatalf("Reset = %+v, %v; Get = %d", info, err, again.Get())
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) != "{}" {
		t.Fatalf("file after reset = %q, %v; want nothing stored", data, err)
	}
}

// A stored value that no longer fits is reported and not applied, and stays stored
// until its own key is set. A stored value nothing has declared is kept.
func TestRejectedAndUndeclaredStoredValues(t *testing.T) {
	path := tempPath(t)
	if err := os.WriteFile(path, []byte(`{"gone.key": 1, "a.count": 500, "a.on": true, "a.null": null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := open(t, path)
	count := settings.Int(s, settings.IntSpec[int]{Meta: meta("a.count"), Default: 5, Min: 1, Max: 100})
	on := settings.Bool(s, settings.BoolSpec{Meta: meta("a.on")})
	nullable := settings.Bool(s, settings.BoolSpec{Meta: meta("a.null"), Default: true})
	if count.Get() != 5 || !on.Get() || !nullable.Get() {
		t.Fatalf("count = %d (want default 5), on = %v (want stored true), nullable = %v (want default true)", count.Get(), on.Get(), nullable.Get())
	}
	info, _ := s.Get("a.count")
	if !strings.Contains(info.StoredError, "500") || info.Stored || info.Origin != settings.OriginDefault {
		t.Fatalf("rejected value info = %+v, want an error and not stored", info)
	}
	if info, _ := s.Get("a.null"); info.StoredError == "" {
		t.Fatalf("stored null info = %+v, want it rejected", info)
	}

	if _, err := s.Set(ctx, "a.on", raw(`false`)); err != nil {
		t.Fatal(err)
	}
	var stored map[string]json.RawMessage
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if string(stored["gone.key"]) != "1" {
		t.Errorf("undeclared value was lost by a write: %s", data)
	}
	if string(stored["a.count"]) != "500" || string(stored["a.on"]) != "false" {
		t.Errorf("stored = %s, want the rejected a.count kept and a.on false", data)
	}
	if info, err := s.Set(ctx, "a.count", raw(`7`)); err != nil || info.StoredError != "" || count.Get() != 7 {
		t.Fatalf("Set should clear the error: %+v, %v, Get = %d", info, err, count.Get())
	}
}

// Declaring a feature's setting after another setting was changed must still find the
// value stored for it.
func TestLateDeclarationKeepsItsStoredValue(t *testing.T) {
	path := tempPath(t)
	if err := os.WriteFile(path, []byte(`{"late.on": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := open(t, path)
	settings.Bool(s, settings.BoolSpec{Meta: meta("early.on")})
	if _, err := s.Set(ctx, "early.on", raw(`true`)); err != nil {
		t.Fatal(err)
	}
	if late := settings.Bool(s, settings.BoolSpec{Meta: meta("late.on")}); !late.Get() {
		t.Fatal("the late setting lost its stored value")
	}
}

func TestChoiceAndListOrder(t *testing.T) {
	s := open(t, tempPath(t))
	mode := settings.Choice(s, settings.ChoiceSpec{
		Meta:    settings.Meta{Key: "b.mode", Title: "Mode", Group: "B"},
		Default: "fast",
		Choices: []settings.ChoiceOption{{Value: "fast", Label: "Fast"}, {Value: "thorough", Label: "Thorough"}},
	})
	settings.Bool(s, settings.BoolSpec{Meta: settings.Meta{Key: "a.z", Title: "Z", Group: "A"}})
	settings.Bool(s, settings.BoolSpec{Meta: settings.Meta{Key: "a.a", Title: "A", Group: "A"}})
	for _, bad := range []string{`"slow"`, `null`, `1`} {
		if _, err := s.Set(ctx, "b.mode", raw(bad)); !errors.Is(err, settings.ErrInvalid) {
			t.Fatalf("Set(%s) = %v, want ErrInvalid", bad, err)
		}
	}
	if _, err := s.Set(ctx, "b.mode", raw(`"thorough"`)); err != nil || mode.Get() != "thorough" {
		t.Fatalf("Set(thorough) = %v, Get = %q", err, mode.Get())
	}
	var keys []string
	for _, info := range s.List() {
		keys = append(keys, info.Key)
	}
	if got, want := strings.Join(keys, " "), "a.a a.z b.mode"; got != want {
		t.Fatalf("List order = %s, want %s", got, want)
	}
}

func TestBoolRejectsNonBooleans(t *testing.T) {
	s := open(t, tempPath(t))
	on := settings.Bool(s, settings.BoolSpec{Meta: meta("a.on"), Default: true})
	for _, bad := range []string{`null`, `1`, `"true"`} {
		if _, err := s.Set(ctx, "a.on", raw(bad)); !errors.Is(err, settings.ErrInvalid) {
			t.Errorf("Set(%s) = %v, want ErrInvalid", bad, err)
		}
	}
	if !on.Get() {
		t.Fatal("a rejected value changed the setting")
	}
}

func TestMalformedDeclarationsPanic(t *testing.T) {
	cases := map[string]func(*settings.Store){
		"default out of range": func(s *settings.Store) {
			settings.Int(s, settings.IntSpec[int]{Meta: meta("x.n"), Default: 0, Min: 1, Max: 2})
		},
		"min above max": func(s *settings.Store) {
			settings.Int(s, settings.IntSpec[int]{Meta: meta("x.n"), Default: 1, Min: 2, Max: 1})
		},
		"duplicate key": func(s *settings.Store) {
			settings.Bool(s, settings.BoolSpec{Meta: meta("x.b")})
			settings.Bool(s, settings.BoolSpec{Meta: meta("x.b")})
		},
		"upper-case key": func(s *settings.Store) { settings.Bool(s, settings.BoolSpec{Meta: meta("X.b")}) },
		"one segment":    func(s *settings.Store) { settings.Bool(s, settings.BoolSpec{Meta: meta("nodots")}) },
		"empty segment":  func(s *settings.Store) { settings.Bool(s, settings.BoolSpec{Meta: meta("a..b")}) },
		"space in key":   func(s *settings.Store) { settings.Bool(s, settings.BoolSpec{Meta: meta("a.b c")}) },
		"no title": func(s *settings.Store) {
			settings.Bool(s, settings.BoolSpec{Meta: settings.Meta{Key: "x.t", Group: "G"}})
		},
		"no group": func(s *settings.Store) {
			settings.Bool(s, settings.BoolSpec{Meta: settings.Meta{Key: "x.g", Title: "G"}})
		},
		"default not a choice": func(s *settings.Store) {
			settings.Choice(s, settings.ChoiceSpec{Meta: meta("x.c"), Default: "b", Choices: []settings.ChoiceOption{{Value: "a"}}})
		},
	}
	for name, declare := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("no panic")
				}
			}()
			declare(open(t, tempPath(t)))
		})
	}
}

func TestDamagedStorageFailsNew(t *testing.T) {
	for _, damaged := range []string{`[1,2]`, ``, `{"a":`} {
		path := tempPath(t)
		if err := os.WriteFile(path, []byte(damaged), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := settings.New(ctx, settings.File(path)); err == nil {
			t.Errorf("New accepted %q", damaged)
		}
	}
	readErr := errors.New("disk on fire")
	if _, err := settings.New(ctx, &memory{readErr: readErr}); !errors.Is(err, readErr) {
		t.Errorf("New = %v, want the storage error", err)
	}
	if _, err := settings.New(ctx, nil); err == nil {
		t.Error("New accepted nil storage")
	}
}

func TestNullDocumentIsEmpty(t *testing.T) {
	s, err := settings.New(ctx, &memory{data: []byte(`null`)})
	if err != nil {
		t.Fatal(err)
	}
	settings.Bool(s, settings.BoolSpec{Meta: meta("a.on")})
	if _, err := s.Set(ctx, "a.on", raw(`true`)); err != nil {
		t.Fatal(err)
	}
}

// If storage fails, the live value, the reported state and the next write are all
// unchanged.
func TestFailedWriteChangesNothing(t *testing.T) {
	m := &memory{data: []byte(`{"a.n": 2}`)}
	s, err := settings.New(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	n := settings.Int(s, settings.IntSpec[int]{Meta: meta("a.n"), Default: 1, Min: 0, Max: 9})

	m.writeErr = errors.New("disk full")
	if _, err := s.Set(ctx, "a.n", raw(`3`)); !errors.Is(err, m.writeErr) {
		t.Fatalf("Set = %v, want the storage error", err)
	}
	if _, err := s.Reset(ctx, "a.n"); !errors.Is(err, m.writeErr) {
		t.Fatalf("Reset = %v, want the storage error", err)
	}
	if n.Get() != 2 {
		t.Fatalf("Get = %d after failed writes, want 2", n.Get())
	}
	if info, _ := s.Get("a.n"); string(info.Value) != "2" || !info.Stored {
		t.Fatalf("info after failed writes = %+v", info)
	}

	m.writeErr = nil
	if _, err := s.Set(ctx, "a.n", raw(`4`)); err != nil || n.Get() != 4 {
		t.Fatalf("Set after recovery = %v, Get = %d", err, n.Get())
	}
}

func TestContextReachesStorage(t *testing.T) {
	type key struct{}
	want := context.WithValue(ctx, key{}, "v")
	m := &memory{}
	s, err := settings.New(want, m)
	if err != nil {
		t.Fatal(err)
	}
	if m.lastCtx != want {
		t.Error("New didn't pass its context to Read")
	}
	settings.Bool(s, settings.BoolSpec{Meta: meta("a.on")})
	m.lastCtx = nil
	if _, err := s.Set(want, "a.on", raw(`true`)); err != nil || m.lastCtx != want {
		t.Errorf("Set didn't pass its context to Write: %v", err)
	}
	m.lastCtx = nil
	if _, err := s.Reset(want, "a.on"); err != nil || m.lastCtx != want {
		t.Errorf("Reset didn't pass its context to Write: %v", err)
	}
}

func TestFileHonoursCancellation(t *testing.T) {
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	path := tempPath(t)
	if _, err := settings.New(cancelled, settings.File(path)); !errors.Is(err, context.Canceled) {
		t.Errorf("New = %v, want context.Canceled", err)
	}
	s := open(t, path)
	on := settings.Bool(s, settings.BoolSpec{Meta: meta("a.on")})
	if _, err := s.Set(cancelled, "a.on", raw(`true`)); !errors.Is(err, context.Canceled) {
		t.Errorf("Set = %v, want context.Canceled", err)
	}
	if on.Get() {
		t.Error("a cancelled Set changed the value")
	}
}

func TestFileCreatesDirectoriesAndLeavesNoTemporaryFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "var", "app")
	path := filepath.Join(dir, "settings.json")
	s := open(t, path)
	settings.Bool(s, settings.BoolSpec{Meta: meta("a.on")})
	for _, v := range []string{`true`, `false`} {
		if _, err := s.Set(ctx, "a.on", raw(v)); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "settings.json" {
		t.Fatalf("directory holds %v, want only settings.json", entries)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("file mode = %o, want 600 because it may hold secrets", st.Mode().Perm())
		}
	}
}

// A string setting nobody has set takes the first environment variable the host sets,
// else its default. A stored value beats both, and resetting returns to the
// environment. A secret's value never leaves the Store.
func TestStringFallsBackToEnvironment(t *testing.T) {
	t.Setenv("FOUNDATION_KIT_TEST_FIRST", "")
	t.Setenv("FOUNDATION_KIT_TEST_SECOND", "  from-env  ")
	s := open(t, tempPath(t))
	key := settings.String(s, settings.StringSpec{
		Meta: meta("a.key"), Env: []string{"FOUNDATION_KIT_TEST_FIRST", "FOUNDATION_KIT_TEST_SECOND"}, Secret: true,
	})
	url := settings.String(s, settings.StringSpec{Meta: meta("a.url"), Default: "https://example.test", Env: []string{"FOUNDATION_KIT_TEST_UNSET"}})
	if key.Get() != "from-env" || url.Get() != "https://example.test" {
		t.Fatalf("key = %q, url = %q; want the env value and the default", key.Get(), url.Get())
	}
	info, _ := s.Get("a.key")
	if info.Origin != settings.OriginEnvironment || info.EnvVar != "FOUNDATION_KIT_TEST_SECOND" || info.Value != nil || !info.HasValue || info.Type != settings.TypeString {
		t.Fatalf("env-backed secret info = %+v", info)
	}
	if info, _ := s.Get("a.url"); info.Origin != settings.OriginDefault || string(info.Value) != `"https://example.test"` || info.EnvVar != "" {
		t.Fatalf("default string info = %+v", info)
	}

	info, err := s.Set(ctx, "a.key", raw(`" typed-key "`))
	if err != nil || key.Get() != "typed-key" || info.Origin != settings.OriginStored || info.Value != nil || !info.HasValue {
		t.Fatalf("Set = %+v, %v; Get = %q", info, err, key.Get())
	}
	for _, bad := range []string{`1`, `"two\nlines"`, `true`, `null`, `"` + strings.Repeat("x", 4097) + `"`} {
		if _, err := s.Set(ctx, "a.key", raw(bad)); !errors.Is(err, settings.ErrInvalid) {
			t.Errorf("Set(%.20s) = %v, want ErrInvalid", bad, err)
		}
	}
	for _, i := range s.List() {
		if b, _ := json.Marshal(i); strings.Contains(string(b), "typed-key") || strings.Contains(string(b), "from-env") {
			t.Fatalf("List serves the secret: %s", b)
		}
	}
	if _, err := s.Set(ctx, "a.key", raw(`""`)); err != nil || key.Get() != "" {
		t.Fatalf("blank value = %v; Get = %q, want blank over the environment", err, key.Get())
	}
	if info, _ := s.Get("a.key"); info.HasValue {
		t.Fatalf("blank secret info = %+v, want no value", info)
	}
	info, err = s.Reset(ctx, "a.key")
	if err != nil || key.Get() != "from-env" || info.Origin != settings.OriginEnvironment {
		t.Fatalf("Reset = %+v, %v; Get = %q, want the env value back", info, err, key.Get())
	}
}

// A secret that no longer fits must not appear in the error reported for it, and
// neither must a rejected new value.
func TestSecretNotLeakedByErrors(t *testing.T) {
	m := &memory{data: []byte(`{"a.key": ["hunter2"]}`)}
	s, err := settings.New(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	settings.String(s, settings.StringSpec{Meta: meta("a.key"), Secret: true})
	info, _ := s.Get("a.key")
	if info.StoredError == "" || strings.Contains(info.StoredError, "hunter2") {
		t.Fatalf("StoredError = %q, want an error without the value", info.StoredError)
	}
	_, err = s.Set(ctx, "a.key", raw(`"hunter2`+`\nsecond line"`))
	if !errors.Is(err, settings.ErrInvalid) || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("Set error = %v, want ErrInvalid without the value", err)
	}
}

func TestInfoReportsTheDeclaration(t *testing.T) {
	s := open(t, tempPath(t))
	m := meta("a.sdk")
	m.Restart, m.Advanced = true, true
	settings.Int(s, settings.IntSpec[int64]{Meta: m, Default: 5, Min: 1, Max: 9, Unit: settings.UnitCount})
	info, _ := s.Get("a.sdk")
	if !info.Restart || !info.Advanced || info.Unit != settings.UnitCount || *info.Min != 1 || *info.Max != 9 || string(info.Default) != "5" || info.Type != settings.TypeInteger {
		t.Fatalf("info = %+v", info)
	}
}

// Changing a returned Info must not change what the Store reports next.
func TestInfoSharesNothingWithTheStore(t *testing.T) {
	s := open(t, tempPath(t))
	settings.Int(s, settings.IntSpec[int]{Meta: meta("a.n"), Default: 5, Min: 1, Max: 9})
	settings.Choice(s, settings.ChoiceSpec{Meta: meta("a.c"), Default: "x", Choices: []settings.ChoiceOption{{Value: "x"}}})
	settings.String(s, settings.StringSpec{Meta: meta("a.s"), Env: []string{"FOUNDATION_KIT_TEST_UNSET"}})

	n, _ := s.Get("a.n")
	*n.Min, *n.Max = 100, 100
	n.Default[0] = '7'
	c, _ := s.Get("a.c")
	c.Choices[0].Value = "changed"
	e, _ := s.Get("a.s")
	e.Env[0] = "changed"

	if n, _ := s.Get("a.n"); *n.Min != 1 || *n.Max != 9 || string(n.Default) != "5" {
		t.Errorf("integer declaration changed: min %d, max %d, default %s", *n.Min, *n.Max, n.Default)
	}
	if c, _ := s.Get("a.c"); c.Choices[0].Value != "x" {
		t.Errorf("choices changed: %+v", c.Choices)
	}
	if e, _ := s.Get("a.s"); e.Env[0] != "FOUNDATION_KIT_TEST_UNSET" {
		t.Errorf("env changed: %v", e.Env)
	}
}

func TestDefinedIntegerTypes(t *testing.T) {
	type retries int
	s := open(t, tempPath(t))
	n := settings.Int(s, settings.IntSpec[retries]{Meta: meta("a.retries"), Default: 3, Min: 0, Max: 9})
	if _, err := s.Set(ctx, "a.retries", raw(`4`)); err != nil {
		t.Fatal(err)
	}
	var got retries = n.Get()
	if got != 4 {
		t.Fatalf("Get = %d, want 4", got)
	}
}

// Run with -race to be meaningful.
func TestConcurrentUse(t *testing.T) {
	s := open(t, tempPath(t))
	n := settings.Int(s, settings.IntSpec[int]{Meta: meta("a.n"), Default: 0, Min: 0, Max: 100})
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 10 {
				if _, err := s.Set(ctx, "a.n", raw(`50`)); err != nil {
					t.Error(err)
				}
				_ = n.Get()
				_ = s.List()
				_, _ = s.Get("a.n")
				settings.Bool(s, settings.BoolSpec{Meta: meta("c.g" + string(rune('a'+i)) + string(rune('a'+j)))})
			}
		}()
	}
	wg.Wait()
	if n.Get() != 50 {
		t.Fatalf("Get = %d, want 50", n.Get())
	}
}

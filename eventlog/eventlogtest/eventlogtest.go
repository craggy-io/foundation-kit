// Package eventlogtest supports tests of code that writes or reads an eventlog:
// an in-memory Store that also serves its objects over HTTP, and a fixed key.
package eventlogtest

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/craggy-io/foundation-kit/eventlog"
)

// Key is a signing key made from a fixed seed, so tests are repeatable. Never
// use it outside tests.
func Key(id string) eventlog.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, id)
	priv := ed25519.NewKeyFromSeed(seed)
	return eventlog.PrivateKey{ID: id, Algorithm: "ed25519",
		Private: base64.StdEncoding.EncodeToString(seed),
		Public:  base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))}
}

// Signer is Key(id)'s signer.
func Signer(id string) eventlog.Signer {
	s, err := Key(id).Signer()
	if err != nil {
		panic(err)
	}
	return s
}

type object struct {
	data       []byte
	generation int64
	options    eventlog.WriteOptions
}

// MemStore is an eventlog.Store in memory, and an http.Handler that serves its
// objects read-only with ETags, as a public bucket does. Safe for concurrent
// use.
type MemStore struct {
	mu      sync.Mutex
	objects map[string]object
	next    int64
	// Fail, when set, is returned by Write for names it returns true for.
	Fail func(name string) error
}

// Read implements eventlog.Store.
func (m *MemStore) Read(_ context.Context, name string) ([]byte, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[name]
	if !ok {
		return nil, 0, fmt.Errorf("%s: %w", name, fs.ErrNotExist)
	}
	return append([]byte(nil), o.data...), o.generation, nil
}

// Write implements eventlog.Store.
func (m *MemStore) Write(_ context.Context, name string, data []byte, o eventlog.WriteOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		if err := m.Fail(name); err != nil {
			return err
		}
	}
	if m.objects == nil {
		m.objects = map[string]object{}
	}
	current := m.objects[name].generation
	if o.IfGeneration != eventlog.AnyGeneration && o.IfGeneration != current {
		return fmt.Errorf("%s is at generation %d, not %d: %w", name, current, o.IfGeneration, eventlog.ErrConflict)
	}
	m.next++
	m.objects[name] = object{data: append([]byte(nil), data...), generation: m.next, options: o}
	return nil
}

// Names lists the stored objects whose names start with prefix.
func (m *MemStore) Names(prefix string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var names []string
	for name := range m.objects {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	return names
}

// Options returns how an object was last written.
func (m *MemStore) Options(name string) eventlog.WriteOptions {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.objects[name].options
}

// Put replaces an object's bytes directly, as tampering or a bad mirror would.
func (m *MemStore) Put(name string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.objects == nil {
		m.objects = map[string]object{}
	}
	m.next++
	o := m.objects[name]
	o.data, o.generation = append([]byte(nil), data...), m.next
	m.objects[name] = o
}

// ServeHTTP serves objects by path, relative to the server's root.
func (m *MemStore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "read only", http.StatusMethodNotAllowed)
		return
	}
	m.mu.Lock()
	o, ok := m.objects[strings.TrimPrefix(r.URL.Path, "/")]
	m.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	etag := `"` + strconv.FormatInt(o.generation, 10) + `"`
	w.Header().Set("ETag", etag)
	if o.options.CacheControl != "" {
		w.Header().Set("Cache-Control", o.options.CacheControl)
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", o.options.ContentType)
	_, _ = w.Write(o.data)
}

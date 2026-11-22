package eventlog_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/craggy-io/foundation-kit/event"
	"github.com/craggy-io/foundation-kit/eventlog"
	"github.com/craggy-io/foundation-kit/eventlog/eventlogtest"
)

var start = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

type env struct {
	t       *testing.T
	store   *eventlogtest.MemStore
	now     time.Time
	writer  *eventlog.Writer
	reader  *eventlog.Reader
	heads   atomic.Int64 // head requests answered 200
	notMods atomic.Int64 // head requests answered 304
	// busy, when set, is asked about each request: true answers it 429
	// with a Retry-After of one second.
	busy func(r *http.Request) bool
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, store: &eventlogtest.MemStore{}, now: start}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e.busy != nil && e.busy(r) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "busy", http.StatusTooManyRequests)
			return
		}
		rec := httptest.NewRecorder()
		e.store.ServeHTTP(rec, r)
		if r.URL.Path == "/"+eventlog.HeadName {
			if rec.Code == http.StatusNotModified {
				e.notMods.Add(1)
			} else {
				e.heads.Add(1)
			}
		}
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}))
	t.Cleanup(server.Close)
	clock := func() time.Time { return e.now }
	e.writer = &eventlog.Writer{Store: e.store, Signer: eventlogtest.Signer("root-1"), Now: clock}
	e.reader = &eventlog.Reader{URL: server.URL + "/", Roots: []eventlog.Key{eventlogtest.Key("root-1").Key()}, Now: clock}
	return e
}

func ev(id, typ, subject string, data string) event.Envelope {
	return event.Envelope{ID: id, Type: typ, Source: "test", Scope: "public", Subject: subject,
		Time: start, Data: json.RawMessage(data)}
}

func (e *env) append(events ...event.Envelope) eventlog.Result {
	e.t.Helper()
	r, err := e.writer.Append(context.Background(), events)
	if err != nil {
		e.t.Fatalf("Append: %v", err)
	}
	return r
}

func (e *env) next(cur eventlog.Cursor) eventlog.Batch {
	e.t.Helper()
	b, err := e.reader.Next(context.Background(), cur)
	if err != nil {
		e.t.Fatalf("Next: %v", err)
	}
	return b
}

func ids(records []eventlog.Record) string {
	var out []string
	for _, r := range records {
		out = append(out, r.Event.ID)
	}
	return strings.Join(out, ",")
}

func TestFollowFromLatestThenRecords(t *testing.T) {
	e := newEnv(t)
	e.append(
		ev("a1", "market.bundle.published@1", "b1", `{"version":"1"}`),
		ev("a2", "market.bundle.published@1", "b1", `{"version":"2"}`),
		ev("a3", "data.dataset.released@1", "d1", `{}`),
	)

	// A new reader starts from the latest state: b1 at version 2, and d1.
	b := e.next(eventlog.Cursor{})
	if !b.Reset || ids(b.Records) != "a3,a2" || b.Cursor.Seq != 3 || b.Cursor.Revision != 1 {
		t.Fatalf("first batch = reset %v, %q, %+v", b.Reset, ids(b.Records), b.Cursor)
	}

	e.append(ev("a4", "market.bundle.failed@1", "b2", `{}`), ev("a5", "data.dataset.released@1", "d1", `{}`))
	b = e.next(b.Cursor)
	if b.Reset || ids(b.Records) != "a4,a5" || b.Cursor.Seq != 5 {
		t.Fatalf("second batch = reset %v, %q, %+v", b.Reset, ids(b.Records), b.Cursor)
	}

	// Nothing new: the head isn't downloaded again.
	before := e.heads.Load()
	b = e.next(b.Cursor)
	if len(b.Records) != 0 || b.Cursor.Seq != 5 || e.heads.Load() != before || e.notMods.Load() != 1 {
		t.Fatalf("idle batch = %q, %+v; heads %d→%d, 304s %d", ids(b.Records), b.Cursor, before, e.heads.Load(), e.notMods.Load())
	}
}

func TestFilter(t *testing.T) {
	e := newEnv(t)
	e.append(ev("x", "market.bundle.published@1", "b1", `{}`))
	cur := e.next(eventlog.Cursor{}).Cursor
	e.append(
		ev("a", "market.bundle.published@1", "b1", `{}`),
		ev("b", "market.index.updated@1", "", `{}`),
		ev("c", "marketing.post.made@1", "p", `{}`),
		ev("d", "data.dataset.released@1", "d1", `{}`),
	)
	for _, tc := range []struct {
		filter eventlog.Filter
		want   string
	}{
		{eventlog.Filter{}, "a,b,c,d"},
		{eventlog.Filter{Types: []string{"market"}}, "a,b"},
		{eventlog.Filter{Types: []string{"market.bundle.published", "data"}}, "a,d"},
		{eventlog.Filter{Subjects: []string{"b1", "d1"}}, "a,d"},
		{eventlog.Filter{Match: func(e event.Envelope) bool { return e.Subject == "" }}, "b"},
	} {
		e.reader.Filter = tc.filter
		if got := ids(e.next(cur).Records); got != tc.want {
			t.Errorf("filter %+v = %q, want %q", tc.filter.Types, got, tc.want)
		}
	}

	// On Reset only the filter's namespaces are read.
	e.reader.Filter = eventlog.Filter{Types: []string{"data"}}
	if b := e.next(eventlog.Cursor{}); !b.Reset || ids(b.Records) != "d" {
		t.Fatalf("filtered reset = %q", ids(b.Records))
	}
}

func TestDuplicatesAreSkipped(t *testing.T) {
	e := newEnv(t)
	e.append(ev("a", "market.bundle.published@1", "b", `{}`))
	r := e.append(ev("a", "market.bundle.published@1", "b", `{}`), ev("b", "market.bundle.published@1", "b", `{}`), ev("b", "market.bundle.published@1", "b", `{}`))
	if ids(r.Added) != "b" || strings.Join(r.Duplicates, ",") != "a,b" {
		t.Fatalf("added %q, duplicates %v", ids(r.Added), r.Duplicates)
	}
	// A new writer (a restarted process) still finds them in the segments.
	fresh := &eventlog.Writer{Store: e.store, Signer: eventlogtest.Signer("root-1"), Now: e.writer.Now}
	r, err := fresh.Append(context.Background(), []event.Envelope{ev("a", "market.bundle.published@1", "b", `{}`)})
	if err != nil || len(r.Added) != 0 || r.Wrote {
		t.Fatalf("fresh writer: %+v, %v", r, err)
	}
}

func TestBadEnvelopesAppendNothing(t *testing.T) {
	e := newEnv(t)
	big := `{"x":"` + strings.Repeat("a", eventlog.MaxDataBytes) + `"}`
	_, err := e.writer.Append(context.Background(), []event.Envelope{
		ev("ok", "market.bundle.published@1", "b", `{}`),
		ev("", "market.bundle.published@1", "b", `{}`),
		ev("big", "market.bundle.published@1", "b", big),
		ev("array", "market.bundle.published@1", "b", `[]`),
	})
	if err == nil || !strings.Contains(err.Error(), "envelope 1") || !strings.Contains(err.Error(), "envelope 2") || !strings.Contains(err.Error(), "envelope 3") {
		t.Fatalf("Append = %v, want every bad envelope named", err)
	}
	if names := e.store.Names(""); len(names) != 0 {
		t.Fatalf("wrote %v", names)
	}
}

func TestReaderRefusesUntrustedLogs(t *testing.T) {
	ctx := context.Background()
	t.Run("rollback", func(t *testing.T) {
		e := newEnv(t)
		e.append(ev("a", "market.bundle.published@1", "b", `{}`))
		old, _, _ := e.store.Read(ctx, eventlog.HeadName)
		e.append(ev("b", "market.bundle.published@1", "b", `{}`))
		cur := e.next(eventlog.Cursor{}).Cursor
		e.store.Put(eventlog.HeadName, old)
		if _, err := e.reader.Next(ctx, cur); !errors.Is(err, eventlog.ErrUntrusted) {
			t.Fatalf("rolled-back head: %v", err)
		}
	})
	t.Run("tampered segment", func(t *testing.T) {
		e := newEnv(t)
		e.append(ev("a", "market.bundle.published@1", "b", `{}`))
		cur := e.next(eventlog.Cursor{}).Cursor
		r := e.append(ev("b", "market.bundle.published@1", "b", `{}`))
		e.store.Put(r.Head.Segments[len(r.Head.Segments)-1].Name, []byte("not it"))
		if _, err := e.reader.Next(ctx, cur); !errors.Is(err, eventlog.ErrUntrusted) {
			t.Fatalf("tampered segment: %v", err)
		}
	})
	t.Run("tampered head", func(t *testing.T) {
		e := newEnv(t)
		e.append(ev("a", "market.bundle.published@1", "b", `{}`))
		data, _, _ := e.store.Read(ctx, eventlog.HeadName)
		e.store.Put(eventlog.HeadName, []byte(strings.Replace(string(data), `"last":1`, `"last":2`, 1)))
		if _, err := e.reader.Next(ctx, eventlog.Cursor{}); !errors.Is(err, eventlog.ErrUntrusted) {
			t.Fatalf("tampered head: %v", err)
		}
	})
	t.Run("unknown root", func(t *testing.T) {
		e := newEnv(t)
		e.append(ev("a", "market.bundle.published@1", "b", `{}`))
		e.reader.Roots = []eventlog.Key{eventlogtest.Key("other").Key()}
		if _, err := e.reader.Next(ctx, eventlog.Cursor{}); !errors.Is(err, eventlog.ErrUntrusted) {
			t.Fatalf("unknown root: %v", err)
		}
	})
	t.Run("expired", func(t *testing.T) {
		e := newEnv(t)
		e.append(ev("a", "market.bundle.published@1", "b", `{}`))
		e.now = e.now.Add(49 * time.Hour)
		if _, err := e.reader.Next(ctx, eventlog.Cursor{}); !errors.Is(err, eventlog.ErrUntrusted) {
			t.Fatalf("expired head: %v", err)
		}
	})
	t.Run("plain http elsewhere", func(t *testing.T) {
		r := eventlog.Reader{URL: "http://example.com/log/"}
		if _, err := r.Next(ctx, eventlog.Cursor{}); !errors.Is(err, eventlog.ErrUnavailable) {
			t.Fatalf("http URL: %v", err)
		}
	})
}

func TestRetentionResetsOldCursors(t *testing.T) {
	e := newEnv(t)
	e.append(ev("a", "market.bundle.published@1", "b1", `{}`))
	cur := e.next(eventlog.Cursor{}).Cursor
	e.now = e.now.Add(31 * 24 * time.Hour)
	r := e.append(ev("b", "market.bundle.published@1", "b2", `{}`))
	if r.Head.First != 2 || len(r.Head.Segments) != 1 {
		t.Fatalf("head after retention: first %d, %d segments", r.Head.First, len(r.Head.Segments))
	}
	// Record 1 is gone, but cur.Seq is 1 = First-1, so nothing is missed.
	if b := e.next(cur); b.Reset || ids(b.Records) != "b" {
		t.Fatalf("batch = reset %v, %q", b.Reset, ids(b.Records))
	}
	// A cursor from before record 1 has missed it, so it gets the latest files,
	// which still has b1.
	if b := e.next(eventlog.Cursor{Revision: 1, Seq: 0}); !b.Reset || ids(b.Records) != "a,b" {
		t.Fatalf("old cursor = reset %v, %q", b.Reset, ids(b.Records))
	}
}

func TestCompactionKeepsEveryRecord(t *testing.T) {
	e := newEnv(t)
	e.writer.MaxSegments = 3
	for i := 1; i <= 7; i++ {
		e.now = e.now.Add(time.Minute)
		r := e.append(ev(fmt.Sprint("e", i), "market.bundle.published@1", fmt.Sprint("b", i), `{}`))
		if len(r.Head.Segments) > 3 {
			t.Fatalf("after %d appends the head lists %d segments", i, len(r.Head.Segments))
		}
	}
	if b := e.next(eventlog.Cursor{Revision: 1, Seq: 2}); b.Reset || ids(b.Records) != "e3,e4,e5,e6,e7" {
		t.Fatalf("after compaction = reset %v, %q", b.Reset, ids(b.Records))
	}
}

func TestHeadIsRefreshedWithoutEvents(t *testing.T) {
	e := newEnv(t)
	if r := e.append(); !r.Wrote || r.Head.Revision != 1 || r.Head.First != 1 || r.Head.Last != 0 {
		t.Fatalf("first empty append should write an empty head: %+v", r)
	}
	first := e.append(ev("a", "market.bundle.published@1", "b", `{}`)).Head
	_, stateGen, _ := e.store.Read(context.Background(), first.Latest[0].Name)

	e.now = e.now.Add(time.Hour)
	if r := e.append(); r.Wrote {
		t.Fatalf("a fresh head was rewritten")
	}
	e.now = e.now.Add(24 * time.Hour)
	r := e.append()
	if !r.Wrote || r.Head.Revision != first.Revision+1 || !r.Head.Expires.Equal(e.now.Add(48*time.Hour)) {
		t.Fatalf("half-expired head: wrote %v, %+v", r.Wrote, r.Head)
	}
	if _, gen, _ := e.store.Read(context.Background(), first.Latest[0].Name); gen == stateGen {
		t.Fatalf("the latest file wasn't renewed")
	}
	if b := e.next(eventlog.Cursor{}); ids(b.Records) != "a" {
		t.Fatalf("after refresh = %q", ids(b.Records))
	}
}

// racingStore lets another writer change the head while an append is running.
type racingStore struct {
	*eventlogtest.MemStore
	race func()
}

func (s *racingStore) Write(ctx context.Context, name string, data []byte, o eventlog.WriteOptions) error {
	if s.race != nil && strings.HasPrefix(name, "seg/") {
		race := s.race
		s.race = nil
		race()
	}
	return s.MemStore.Write(ctx, name, data, o)
}

func TestConcurrentWritersConflict(t *testing.T) {
	e := newEnv(t)
	e.append(ev("a", "market.bundle.published@1", "b", `{}`))
	store := &racingStore{MemStore: e.store}
	store.race = func() {
		other := &eventlog.Writer{Store: e.store, Signer: eventlogtest.Signer("root-1"), Now: e.writer.Now}
		if _, err := other.Append(context.Background(), []event.Envelope{ev("other", "market.bundle.published@1", "b", `{}`)}); err != nil {
			t.Errorf("other writer: %v", err)
		}
	}
	w := &eventlog.Writer{Store: store, Signer: eventlogtest.Signer("root-1"), Now: e.writer.Now}
	_, err := w.Append(context.Background(), []event.Envelope{ev("mine", "market.bundle.published@1", "b", `{}`)})
	if !errors.Is(err, eventlog.ErrConflict) {
		t.Fatalf("losing writer: %v", err)
	}
	// The log is the winner's, intact.
	if b := e.next(eventlog.Cursor{Revision: 1, Seq: 1}); ids(b.Records) != "other" {
		t.Fatalf("after conflict = %q", ids(b.Records))
	}
	// Appending again succeeds on top of it.
	if r := e.append(ev("mine", "market.bundle.published@1", "b", `{}`)); r.Head.Last != 3 {
		t.Fatalf("retry: last %d", r.Head.Last)
	}
}

func TestObjectsAreServedForCaching(t *testing.T) {
	e := newEnv(t)
	r := e.append(ev("a", "market.bundle.published@1", "b", `{}`))
	if cc := e.store.Options(eventlog.HeadName).CacheControl; !strings.Contains(cc, "max-age=30") {
		t.Errorf("head Cache-Control %q", cc)
	}
	for _, name := range []string{r.Head.Segments[0].Name, r.Head.Latest[0].Name} {
		if cc := e.store.Options(name).CacheControl; !strings.Contains(cc, "immutable") {
			t.Errorf("%s Cache-Control %q", name, cc)
		}
	}
}

func TestParseRoots(t *testing.T) {
	keys, err := eventlog.ParseRoots(" a:AAAA , b:BBBB,")
	if err != nil || len(keys) != 2 || keys[1].ID != "b" || keys[1].Public != "BBBB" {
		t.Fatalf("ParseRoots = %+v, %v", keys, err)
	}
	if _, err := eventlog.ParseRoots("nocolon"); err == nil {
		t.Fatal("ParseRoots accepted a key without an ID")
	}
	k, err := eventlog.GenerateKey("fresh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.Signer(); err != nil {
		t.Fatal(err)
	}
}

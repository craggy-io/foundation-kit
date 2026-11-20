package eventlog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/craggy-io/foundation-kit/event"
)

// Cursor is how far a reader has followed a log. The zero Cursor has read
// nothing, so its first Next is a Reset with the latest state rather than a
// replay of history. Keep it between runs: it is what lets Next return only
// what is new.
type Cursor struct {
	// Revision is the revision of the last head accepted; an older head is
	// refused as a rollback.
	Revision int64 `json:"revision"`
	// Seq is the sequence of the last record read.
	Seq int64 `json:"seq"`
}

// Batch is what one Next returned.
type Batch struct {
	// Cursor is where to read from next time.
	Cursor Cursor
	// Reset reports that the log no longer holds the records after the cursor
	// (or never did), so Records are the namespaces' latest state, not a
	// continuation: replace whatever was known from earlier batches.
	Reset bool
	// Records are the matching records, in sequence order. With Reset they are
	// in latest-file order: by namespace, type and subject.
	Records []Record
}

// Filter selects records. The zero Filter matches everything.
type Filter struct {
	// Types are event types without versions, or prefixes of them that end at
	// a dot: "market" matches "market.bundle.published@1", and so does
	// "market.bundle.published". Empty matches every type. A filter with
	// types reads only their namespaces' latest files on Reset.
	Types []string
	// Subjects, when set, are the only subjects that match.
	Subjects []string
	// Match, when set, must also accept the envelope: for predicates the
	// fields above can't express, such as trigger specs or a taxonomy.
	Match func(event.Envelope) bool
}

// Matches reports whether the filter selects an envelope.
func (f Filter) Matches(e event.Envelope) bool {
	if len(f.Types) > 0 {
		name := TypeName(e.Type)
		if !slices.ContainsFunc(f.Types, func(t string) bool { return name == t || strings.HasPrefix(name, t+".") }) {
			return false
		}
	}
	if len(f.Subjects) > 0 && !slices.Contains(f.Subjects, e.Subject) {
		return false
	}
	return f.Match == nil || f.Match(e)
}

// namespaces are the latest files a filter needs, or nil for all of them.
func (f Filter) namespaces() map[string]bool {
	if len(f.Types) == 0 {
		return nil
	}
	ns := map[string]bool{}
	for _, t := range f.Types {
		ns[Namespace(t)] = true
	}
	return ns
}

// Reader follows one log over HTTP.
type Reader struct {
	// URL is the log's root; the head is URL + "head.json". It must be https,
	// except on this machine.
	URL string
	// Roots are the keys a head may be signed by.
	Roots  []Key
	Filter Filter
	HTTP   *http.Client
	Now    func() time.Time

	// The last head fetched, so an unchanged head costs a 304, and the
	// segments read, which never change.
	etag     string
	head     []byte
	segments map[string][]Record
}

// maxCachedSegments bounds the segments a Reader keeps decoded.
const maxCachedSegments = 32

func (r *Reader) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Reader) client() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{Timeout: time.Minute}
}

// Next returns what the log holds after the cursor that the filter matches.
// Unreachable logs are ErrUnavailable; anything that fails a check is
// ErrUntrusted, and the cursor should be kept as it was.
func (r *Reader) Next(ctx context.Context, cur Cursor) (Batch, error) {
	base, head, err := r.verifiedHead(ctx)
	if err != nil {
		return Batch{}, err
	}
	if head.Revision < cur.Revision {
		return Batch{}, fmt.Errorf("%w: the head is revision %d, older than revision %d already read", ErrUntrusted, head.Revision, cur.Revision)
	}
	next := Cursor{Revision: head.Revision, Seq: head.Last}
	if cur.Revision == 0 || cur.Seq < head.First-1 || cur.Seq > head.Last {
		records, err := r.latest(ctx, base, head)
		return Batch{Cursor: next, Reset: true, Records: records}, err
	}
	var records []Record
	for _, ref := range head.Segments {
		if ref.Last <= cur.Seq {
			continue
		}
		rs, err := r.segment(ctx, base, ref)
		if err != nil {
			return Batch{}, err
		}
		for _, rec := range rs {
			if rec.Seq > cur.Seq && r.Filter.Matches(rec.Event) {
				records = append(records, rec)
			}
		}
	}
	return Batch{Cursor: next, Records: records}, nil
}

// Tail returns up to n of the newest records the filter matches, oldest
// first, and the cursor at the head they were read from: for showing recent
// history, such as after a restart. Records the log no longer holds are not
// in it; the latest files are, through Next.
func (r *Reader) Tail(ctx context.Context, n int) ([]Record, Cursor, error) {
	base, head, err := r.verifiedHead(ctx)
	if err != nil {
		return nil, Cursor{}, err
	}
	var newest []Record // newest first
	for i := len(head.Segments) - 1; i >= 0 && len(newest) < n; i-- {
		rs, err := r.segment(ctx, base, head.Segments[i])
		if err != nil {
			return nil, Cursor{}, err
		}
		for j := len(rs) - 1; j >= 0 && len(newest) < n; j-- {
			if r.Filter.Matches(rs[j].Event) {
				newest = append(newest, rs[j])
			}
		}
	}
	slices.Reverse(newest)
	return newest, Cursor{Revision: head.Revision, Seq: head.Last}, nil
}

// verifiedHead fetches and checks the head.
func (r *Reader) verifiedHead(ctx context.Context) (*url.URL, Head, error) {
	base, err := r.base()
	if err != nil {
		return nil, Head{}, err
	}
	data, err := r.fetchHead(ctx, base)
	if err != nil {
		return nil, Head{}, err
	}
	head, err := VerifyHead(data, r.Roots, r.now())
	return base, head, err
}

// segment reads one segment the head lists, once: segments never change.
func (r *Reader) segment(ctx context.Context, base *url.URL, ref SegmentRef) ([]Record, error) {
	if rs, ok := r.segments[ref.Digest]; ok {
		return rs, nil
	}
	data, err := r.fetch(ctx, base, ref.Name, ref.Bytes)
	if err != nil {
		return nil, err
	}
	rs, err := decodeSegment(data, ref)
	if err != nil {
		return nil, err
	}
	if r.segments == nil || len(r.segments) >= maxCachedSegments {
		r.segments = map[string][]Record{}
	}
	r.segments[ref.Digest] = rs
	return rs, nil
}

func (r *Reader) latest(ctx context.Context, base *url.URL, head Head) ([]Record, error) {
	wanted := r.Filter.namespaces()
	var records []Record
	for _, ref := range head.Latest {
		if wanted != nil && !wanted[ref.Namespace] {
			continue
		}
		data, err := r.fetch(ctx, base, ref.Name, ref.Bytes)
		if err != nil {
			return nil, err
		}
		file, err := decodeLatest(data, ref)
		if err != nil {
			return nil, err
		}
		for _, rec := range file.Records {
			if r.Filter.Matches(rec.Event) {
				records = append(records, rec)
			}
		}
	}
	return records, nil
}

// base checks the log's address.
func (r *Reader) base() (*url.URL, error) {
	u, err := url.Parse(r.URL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: %q is not an address", ErrUnavailable, r.URL)
	}
	host := u.Hostname()
	local := host == "127.0.0.1" || host == "::1" || host == "localhost"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return nil, fmt.Errorf("%w: %s is not https", ErrUnavailable, r.URL)
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return u, nil
}

func (r *Reader) fetchHead(ctx context.Context, base *url.URL) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.JoinPath(HeadName).String(), nil)
	if err != nil {
		return nil, err
	}
	if r.etag != "" && r.head != nil {
		req.Header.Set("If-None-Match", r.etag)
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified && r.head != nil {
		return r.head, nil
	}
	data, err := readBody(resp, maxHeadBytes)
	if err != nil {
		return nil, err
	}
	r.etag, r.head = resp.Header.Get("ETag"), data
	return data, nil
}

// fetch reads one file the head lists. Its name was checked with the head, so
// it can't leave the log's root.
func (r *Reader) fetch(ctx context.Context, base *url.URL, name string, size int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.JoinPath(name).String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	return readBody(resp, size)
}

func readBody(resp *http.Response, limit int64) ([]byte, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s answered %s", ErrUnavailable, resp.Request.URL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrUntrusted, resp.Request.URL, limit)
	}
	return data, nil
}

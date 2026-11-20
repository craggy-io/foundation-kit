package eventlog

import (
	"bufio"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/craggy-io/foundation-kit/event"
)

// AnyGeneration is a write that replaces whatever is there.
const AnyGeneration int64 = -1

// Store is where a writer keeps a log's objects, by name relative to the log's
// root. A bucket, a folder or memory.
type Store interface {
	// Read returns an object and its generation. A missing object is an error
	// matching fs.ErrNotExist.
	Read(ctx context.Context, name string) (data []byte, generation int64, err error)
	// Write stores an object if its generation is o.IfGeneration: 0 means it
	// must not exist, AnyGeneration means any. Otherwise it fails with an error
	// matching ErrConflict.
	Write(ctx context.Context, name string, data []byte, o WriteOptions) error
}

// WriteOptions say how an object is written and served.
type WriteOptions struct {
	IfGeneration int64
	ContentType  string
	CacheControl string
}

// How each object is served. Segments never change; the head and latest files
// are rewritten, so readers cache them briefly.
const (
	mutableCache   = "public, max-age=30, no-transform"
	immutableCache = "public, max-age=31536000, immutable, no-transform"
)

// Defaults for a Writer's zero fields.
const (
	defaultRetain       = 30 * 24 * time.Hour
	defaultHeadTTL      = 48 * time.Hour
	defaultMaxSegments  = 128
	defaultSegmentBytes = 1 << 20
	defaultDedupe       = 24 * time.Hour
)

// Writer appends to one log. It is the log's only writer: run one per log, and
// let the store's preconditions refuse a second.
type Writer struct {
	Store  Store
	Signer Signer
	Now    func() time.Time
	// Retain is how long a segment stays in the head after its newest record
	// (default 30 days). Delete segment objects from the store some days after
	// that, never sooner.
	Retain time.Duration
	// HeadTTL is how long a head is valid (default 48 hours). Append rewrites
	// the head once half of it has passed, even with nothing to add, so call
	// it at least that often.
	HeadTTL time.Duration
	// MaxSegments is how many segments a head lists before the oldest are
	// merged (default 128), and SegmentBytes the size merges aim for (1 MiB).
	MaxSegments  int
	SegmentBytes int64
	// Dedupe is how far back an envelope ID is looked for before it is
	// appended again (default 24 hours).
	Dedupe time.Duration

	ids map[string][]string // segment name → envelope IDs, for deduplication
}

// Result is what one Append did.
type Result struct {
	Head Head
	// Added are the records appended; Duplicates the envelope IDs skipped
	// because the log already held them.
	Added      []Record
	Duplicates []string
	// Wrote reports whether a new head was written.
	Wrote bool
}

func (w *Writer) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}

func or[T comparable](v, fallback T) T {
	var zero T
	if v == zero {
		return fallback
	}
	return v
}

// Append adds envelopes to the log as records, in order, skipping any whose ID
// the log already holds. Every envelope must pass event.Validate and carry no
// more than MaxDataBytes of data, or nothing is appended. With nothing to add,
// Append still rewrites the head when it is half expired, when there is none
// yet, or when segments are due to be dropped or merged.
func (w *Writer) Append(ctx context.Context, events []event.Envelope) (Result, error) {
	if err := checkEvents(events); err != nil {
		return Result{}, err
	}
	now := w.now()
	head, generation, err := w.readHead(ctx)
	if err != nil {
		return Result{}, err
	}
	seen, err := w.recentIDs(ctx, head, now)
	if err != nil {
		return Result{}, err
	}
	var result Result
	for _, e := range events {
		if seen[e.ID] {
			result.Duplicates = append(result.Duplicates, e.ID)
			continue
		}
		seen[e.ID] = true
		result.Added = append(result.Added, Record{Seq: head.Last + int64(len(result.Added)) + 1, Logged: now, Event: e})
	}

	written := map[string]bool{}
	if len(result.Added) > 0 {
		ref, err := w.writeSegment(ctx, result.Added)
		if err != nil {
			return Result{}, err
		}
		head.Segments = append(head.Segments, ref)
		head.Last = ref.Last
		if written, err = w.updateLatest(ctx, &head, result.Added); err != nil {
			return Result{}, err
		}
	}
	dropped := w.retain(&head, now)
	merged := len(head.Segments) > or(w.MaxSegments, defaultMaxSegments)
	if merged {
		if err := w.compact(ctx, &head); err != nil {
			return Result{}, err
		}
	}
	ttl := or(w.HeadTTL, defaultHeadTTL)
	stale := generation == 0 || head.Expires.Sub(now) < ttl/2
	if len(result.Added) == 0 && !dropped && !merged && !stale {
		result.Head = head
		return result, nil
	}

	if stale {
		if err := w.renewLatest(ctx, head, written); err != nil {
			return Result{}, err
		}
	}
	head.Revision++
	head.Written, head.Expires = now, now.Add(ttl)
	if len(head.Segments) > 0 {
		head.First = head.Segments[0].First
	} else {
		head.First = head.Last + 1
	}
	if err := head.check(); err != nil {
		return Result{}, fmt.Errorf("eventlog: refusing to write a bad head: %w", err)
	}
	data, err := signHead(ctx, w.Signer, head)
	if err != nil {
		return Result{}, err
	}
	if err := w.Store.Write(ctx, HeadName, data, WriteOptions{IfGeneration: generation, ContentType: "application/json", CacheControl: mutableCache}); err != nil {
		return Result{}, fmt.Errorf("eventlog: writing the head: %w", err)
	}
	result.Head, result.Wrote = head, true
	return result, nil
}

// checkEvents reports every envelope that can't go in the log.
func checkEvents(events []event.Envelope) error {
	var problems []string
	for i, e := range events {
		if err := e.Validate(); err != nil {
			problems = append(problems, fmt.Sprintf("envelope %d (%q): %v", i, e.ID, err))
		} else if len(e.Data) > MaxDataBytes {
			problems = append(problems, fmt.Sprintf("envelope %d (%q): data is %d bytes, over %d", i, e.ID, len(e.Data), MaxDataBytes))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("eventlog: %s", strings.Join(problems, "; "))
	}
	return nil
}

func (w *Writer) readHead(ctx context.Context) (Head, int64, error) {
	data, generation, err := w.Store.Read(ctx, HeadName)
	if errors.Is(err, fs.ErrNotExist) {
		return Head{Format: headFormat, First: 1}, 0, nil
	}
	if err != nil {
		return Head{}, 0, fmt.Errorf("eventlog: reading the head: %w", err)
	}
	_, head, err := decodeHead(data)
	return head, generation, err
}

// recentIDs collects the envelope IDs of segments within the dedupe window,
// reading each segment once per Writer.
func (w *Writer) recentIDs(ctx context.Context, head Head, now time.Time) (map[string]bool, error) {
	if w.ids == nil {
		w.ids = map[string][]string{}
	}
	since := now.Add(-or(w.Dedupe, defaultDedupe))
	seen := map[string]bool{}
	keep := map[string][]string{}
	for _, ref := range head.Segments {
		if ref.Newest.Before(since) {
			continue
		}
		ids, ok := w.ids[ref.Name]
		if !ok {
			records, err := w.readSegment(ctx, ref)
			if err != nil {
				return nil, err
			}
			for _, r := range records {
				ids = append(ids, r.Event.ID)
			}
		}
		keep[ref.Name] = ids
		for _, id := range ids {
			seen[id] = true
		}
	}
	w.ids = keep
	return seen, nil
}

func (w *Writer) writeSegment(ctx context.Context, records []Record) (SegmentRef, error) {
	data, err := encodeSegment(records)
	if err != nil {
		return SegmentRef{}, err
	}
	digest := digestOf(data)
	first, last := records[0].Seq, records[len(records)-1].Seq
	ref := SegmentRef{Name: segmentName(first, last, digest), First: first, Last: last, Count: len(records),
		Newest: records[len(records)-1].Logged, Bytes: int64(len(data)), Digest: digest}
	// Named after its content, so rewriting one is harmless.
	if err := w.Store.Write(ctx, ref.Name, data, WriteOptions{IfGeneration: AnyGeneration, ContentType: "application/gzip", CacheControl: immutableCache}); err != nil {
		return SegmentRef{}, fmt.Errorf("eventlog: writing %s: %w", ref.Name, err)
	}
	if w.ids != nil {
		for _, r := range records {
			w.ids[ref.Name] = append(w.ids[ref.Name], r.Event.ID)
		}
	}
	return ref, nil
}

func (w *Writer) readSegment(ctx context.Context, ref SegmentRef) ([]Record, error) {
	data, _, err := w.Store.Read(ctx, ref.Name)
	if err != nil {
		return nil, fmt.Errorf("eventlog: reading %s: %w", ref.Name, err)
	}
	return decodeSegment(data, ref)
}

// updateLatest merges the new records into each touched namespace's state.
// It returns the names of the latest files it wrote.
func (w *Writer) updateLatest(ctx context.Context, head *Head, records []Record) (map[string]bool, error) {
	written := map[string]bool{}
	byNamespace := map[string][]Record{}
	for _, r := range records {
		ns := Namespace(r.Event.Type)
		byNamespace[ns] = append(byNamespace[ns], r)
	}
	for _, ns := range sortedKeys(byNamespace) {
		latest := Latest{Format: latestFormat, Namespace: ns}
		i := slices.IndexFunc(head.Latest, func(s LatestRef) bool { return s.Namespace == ns })
		if i >= 0 {
			data, _, err := w.Store.Read(ctx, head.Latest[i].Name)
			if err != nil {
				return nil, fmt.Errorf("eventlog: reading the %s latest file: %w", ns, err)
			}
			if latest, err = decodeLatest(data, head.Latest[i]); err != nil {
				return nil, err
			}
		}
		latest.Records = mergeLatest(latest.Records, byNamespace[ns])
		latest.Last = byNamespace[ns][len(byNamespace[ns])-1].Seq
		data, err := json.Marshal(latest)
		if err != nil {
			return nil, err
		}
		digest := digestOf(data)
		ref := LatestRef{Namespace: ns, Name: latestName(ns, digest), Last: latest.Last, Count: len(latest.Records), Bytes: int64(len(data)), Digest: digest}
		if ref.Bytes > maxLatestBytes {
			return nil, fmt.Errorf("eventlog: the %s latest file would be %d bytes, over %d: it has too many subjects", ns, ref.Bytes, maxLatestBytes)
		}
		if err := w.writeLatest(ctx, ref.Name, data); err != nil {
			return nil, err
		}
		written[ref.Name] = true
		if i >= 0 {
			head.Latest[i] = ref
		} else {
			head.Latest = append(head.Latest, ref)
			slices.SortFunc(head.Latest, func(a, b LatestRef) int { return cmp.Compare(a.Namespace, b.Namespace) })
		}
	}
	return written, nil
}

// writeLatest stores a latest file. It is named after its content, so writing
// it again only renews its age in the store.
func (w *Writer) writeLatest(ctx context.Context, name string, data []byte) error {
	if err := w.Store.Write(ctx, name, data, WriteOptions{IfGeneration: AnyGeneration, ContentType: "application/json", CacheControl: immutableCache}); err != nil {
		return fmt.Errorf("eventlog: writing %s: %w", name, err)
	}
	return nil
}

// renewLatest rewrites the latest files the head lists that this append didn't
// write, so age-based deletion in the store never reaches them.
func (w *Writer) renewLatest(ctx context.Context, head Head, written map[string]bool) error {
	for _, ref := range head.Latest {
		if written[ref.Name] {
			continue
		}
		data, _, err := w.Store.Read(ctx, ref.Name)
		if err != nil {
			return fmt.Errorf("eventlog: reading %s: %w", ref.Name, err)
		}
		if _, err := decodeLatest(data, ref); err != nil {
			return err
		}
		if err := w.writeLatest(ctx, ref.Name, data); err != nil {
			return err
		}
	}
	return nil
}

func latestKey(r Record) string { return TypeName(r.Event.Type) + "\n" + r.Event.Subject }

// mergeLatest keeps the latest record per type and subject.
func mergeLatest(current, added []Record) []Record {
	latest := map[string]Record{}
	for _, r := range current {
		latest[latestKey(r)] = r
	}
	for _, r := range added {
		latest[latestKey(r)] = r
	}
	out := make([]Record, 0, len(latest))
	for _, key := range sortedKeys(latest) {
		out = append(out, latest[key])
	}
	return out
}

// retain drops segments whose newest record is older than the retention.
func (w *Writer) retain(head *Head, now time.Time) bool {
	cutoff := now.Add(-or(w.Retain, defaultRetain))
	n := 0
	for n < len(head.Segments) && head.Segments[n].Newest.Before(cutoff) {
		n++
	}
	head.Segments = head.Segments[n:]
	return n > 0
}

// compact merges the oldest segments until the head lists no more than
// MaxSegments: each merge takes the longest run from the oldest that fits in
// SegmentBytes, and at least two.
func (w *Writer) compact(ctx context.Context, head *Head) error {
	limit, target := or(w.MaxSegments, defaultMaxSegments), or(w.SegmentBytes, defaultSegmentBytes)
	start := 0
	for len(head.Segments) > limit && start < len(head.Segments)-1 {
		end, size := start, int64(0)
		for end < len(head.Segments) && (end-start < 2 || size+head.Segments[end].Bytes <= target) {
			size += head.Segments[end].Bytes
			end++
		}
		var records []Record
		for _, ref := range head.Segments[start:end] {
			rs, err := w.readSegment(ctx, ref)
			if err != nil {
				return err
			}
			records = append(records, rs...)
		}
		ref, err := w.writeSegment(ctx, records)
		if err != nil {
			return err
		}
		head.Segments = slices.Replace(head.Segments, start, end, ref)
		start++
	}
	return nil
}

func encodeSegment(records []Record) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	enc := json.NewEncoder(zw)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decodeSegment checks a segment against its reference and reads its records.
func decodeSegment(data []byte, ref SegmentRef) ([]Record, error) {
	if int64(len(data)) != ref.Bytes || digestOf(data) != ref.Digest {
		return nil, fmt.Errorf("%w: %s is not the segment the head lists", ErrUntrusted, ref.Name)
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrUntrusted, ref.Name, err)
	}
	var records []Record
	scanner := bufio.NewScanner(io.LimitReader(zr, 8*maxSegmentBytes))
	scanner.Buffer(nil, 2*MaxDataBytes+64<<10)
	for scanner.Scan() {
		var r Record
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrUntrusted, ref.Name, err)
		}
		records = append(records, r)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrUntrusted, ref.Name, err)
	}
	if len(records) != ref.Count {
		return nil, fmt.Errorf("%w: %s holds %d records, not %d", ErrUntrusted, ref.Name, len(records), ref.Count)
	}
	for i, r := range records {
		if r.Seq != ref.First+int64(i) {
			return nil, fmt.Errorf("%w: %s holds sequence %d out of place", ErrUntrusted, ref.Name, r.Seq)
		}
	}
	return records, nil
}

func decodeLatest(data []byte, ref LatestRef) (Latest, error) {
	if int64(len(data)) != ref.Bytes || digestOf(data) != ref.Digest {
		return Latest{}, fmt.Errorf("%w: %s is not the state the head lists", ErrUntrusted, ref.Name)
	}
	var s Latest
	if err := json.Unmarshal(data, &s); err != nil || s.Format != latestFormat || s.Namespace != ref.Namespace {
		return Latest{}, fmt.Errorf("%w: %s can't be read", ErrUntrusted, ref.Name)
	}
	return s, nil
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

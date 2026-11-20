package eventlog

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/craggy-io/foundation-kit/event"
)

// Errors a caller acts on. Match them with errors.Is.
var (
	// ErrUntrusted is a log that failed a check: a signature, a digest, a
	// rolled-back or expired head, or a file that isn't what the head said.
	ErrUntrusted = errors.New("eventlog: not trusted")
	// ErrUnavailable is a log that couldn't be reached or answered wrongly.
	ErrUnavailable = errors.New("eventlog: unavailable")
	// ErrConflict is a write that lost to another writer. Nothing it wrote is
	// referenced; append again.
	ErrConflict = errors.New("eventlog: changed by another writer")
)

// HeadName is the head's object name, relative to the log's root.
const HeadName = "head.json"

// MaxDataBytes bounds one envelope's data, so that a log stays small enough
// for every reader to download.
const MaxDataBytes = 4 << 10

const (
	headFormat   = "eventlog.head@1"
	latestFormat = "eventlog.latest@1"
)

// Limits on what a reader downloads, so a wrong or hostile server can't
// exhaust its memory.
const (
	maxHeadBytes    = 1 << 20
	maxSegmentBytes = 32 << 20
	maxLatestBytes  = 32 << 20
)

// Record is one envelope as the log numbered it.
type Record struct {
	Seq int64 `json:"seq"`
	// Logged is when the writer appended it. The envelope's own Time is when
	// its producer says it happened.
	Logged time.Time      `json:"logged"`
	Event  event.Envelope `json:"event"`
}

// SegmentRef is a head's entry for one segment.
type SegmentRef struct {
	Name  string `json:"name"`
	First int64  `json:"first"`
	Last  int64  `json:"last"`
	Count int    `json:"count"`
	// Newest is the Logged time of its last record; retention is decided by it.
	Newest time.Time `json:"newest"`
	Bytes  int64     `json:"bytes"`
	Digest string    `json:"digest"`
}

// LatestRef is a head's entry for one namespace's latest file.
type LatestRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Last      int64  `json:"last"`
	Count     int    `json:"count"`
	Bytes     int64  `json:"bytes"`
	Digest    string `json:"digest"`
}

// Head is what a log holds. First is the first sequence still in a segment;
// when no segment is left it is Last+1.
type Head struct {
	Format   string       `json:"format"`
	Revision int64        `json:"revision"`
	First    int64        `json:"first"`
	Last     int64        `json:"last"`
	Written  time.Time    `json:"written"`
	Expires  time.Time    `json:"expires"`
	Segments []SegmentRef `json:"segments"`
	Latest   []LatestRef  `json:"latest"`
}

// Latest is the latest record of each type (without its version) and subject
// in one namespace, sorted by type and subject.
type Latest struct {
	Format    string   `json:"format"`
	Namespace string   `json:"namespace"`
	Last      int64    `json:"last"`
	Records   []Record `json:"records"`
}

var (
	segmentRE   = regexp.MustCompile(`^seg/[0-9]{12}-[0-9]{12}-[0-9a-f]{12}\.ndjson\.gz$`)
	latestRE    = regexp.MustCompile(`^latest/[a-z][a-z0-9_-]*-[0-9a-f]{12}\.json$`)
	digestRE    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	namespaceRE = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
)

func segmentName(first, last int64, digest string) string {
	return fmt.Sprintf("seg/%012d-%012d-%s.ndjson.gz", first, last, strings.TrimPrefix(digest, "sha256:")[:12])
}

func latestName(namespace, digest string) string {
	return "latest/" + namespace + "-" + strings.TrimPrefix(digest, "sha256:")[:12] + ".json"
}

// TypeName is an event type without its version: "a.b.c@2" is "a.b.c".
func TypeName(eventType string) string {
	name, _, _ := strings.Cut(eventType, "@")
	return name
}

// Namespace is the first segment of an event type: "a.b.c@2" is "a".
func Namespace(eventType string) string {
	ns, _, _ := strings.Cut(TypeName(eventType), ".")
	return ns
}

// check reports every structural problem with a head, so that a reader acts
// only on a head whose references it may follow.
func (h Head) check() error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	if h.Format != headFormat {
		add("format is %q, not %q", h.Format, headFormat)
	}
	if h.Revision < 1 || h.Last < 0 || h.First < 1 || h.First > h.Last+1 {
		add("revision %d, first %d and last %d are inconsistent", h.Revision, h.First, h.Last)
	}
	next := h.First
	for _, s := range h.Segments {
		if !segmentRE.MatchString(s.Name) || !digestRE.MatchString(s.Digest) {
			add("segment %q has a bad name or digest", s.Name)
		}
		if s.First != next || s.Last < s.First || s.Count != int(s.Last-s.First+1) || s.Bytes <= 0 || s.Bytes > maxSegmentBytes {
			add("segment %q doesn't follow on from sequence %d", s.Name, next-1)
		}
		next = s.Last + 1
	}
	if next != h.Last+1 {
		add("segments end at %d, not at the last sequence %d", next-1, h.Last)
	}
	seen := map[string]bool{}
	for _, s := range h.Latest {
		if !namespaceRE.MatchString(s.Namespace) || !digestRE.MatchString(s.Digest) || s.Name != latestName(s.Namespace, s.Digest) || !latestRE.MatchString(s.Name) || seen[s.Namespace] || s.Last > h.Last || s.Bytes <= 0 || s.Bytes > maxLatestBytes {
			add("latest file %q is malformed", s.Namespace)
		}
		seen[s.Namespace] = true
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: the head: %s", ErrUntrusted, strings.Join(problems, "; "))
	}
	return nil
}

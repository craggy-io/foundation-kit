// Package eventlog is a signed, append-only log of event envelopes, kept as
// static files that anyone can read and only one writer can change.
//
// A log is a set of objects under one root, in a Store the writer owns, served
// read-only over HTTP (a public bucket, a CDN, a local folder):
//
//	head.json                       the signed head: what the log holds now
//	seg/<first>-<last>-<hash>.ndjson.gz  immutable segments of records
//	latest/<namespace>-<hash>.json  the latest record per type and subject
//
// The head is the only object a reader trusts directly: it is signed with an
// Ed25519 key, and it lists every segment and latest file with its size and
// SHA-256 digest, so one signature covers everything. A Writer appends
// envelopes as numbered records, writes a new segment, updates the latest files
// of the namespaces it touched, drops segments older than its retention,
// merges segments when there are too many, and writes the head last.
//
// A Reader follows the log with a Cursor. When the records after its cursor are
// still in the log, Next returns them. When they are not (the cursor is older
// than the retention, or ahead of the log), Next returns the latest files
// instead with Reset set, and the reader replaces whatever it knew. Records
// announce that something changed; readers that need the new data fetch it
// from its owner.
//
// # Invariants
//
//   - Sequences start at 1 and increase by one per record, without gaps.
//   - The head's Revision increases by one per head written. A Reader refuses a
//     head older than the revision in its cursor, and a head past its Expires.
//   - Segments and latest files are named after their content and never
//     change. Only the head is rewritten, so a failed or lost write leaves the
//     published log as it was.
//   - Each head refresh rewrites the current latest files, so a store that
//     deletes objects by age (a bucket lifecycle rule) only ever deletes
//     objects no head lists, as long as the delay is longer than Retain.
//   - Segments listed in a head are contiguous: together they hold every record
//     from First to Last.
//   - A Writer writes the head with the store generation it read, so two
//     writers can never both succeed; the loser gets ErrConflict.
//
// # Concurrency
//
// A Writer or a Reader is used by one goroutine at a time: each caches what it
// has read. Several processes may share a log; the store's preconditions keep
// writers apart. Readers only ever read.
//
// A Follower polls a Reader on a timer and saves the cursor (in a FileCursor)
// after its handler accepts each batch. Reader.Tail returns the newest records.
//
// Exported names: Record, Head, SegmentRef, LatestRef, Latest, Cursor, Batch,
// Filter, Follower, Status, CursorStore, FileCursor, Store, WriteOptions, AnyGeneration, Writer, Result, Reader, Signer,
// Key, Signature, PrivateKey, GenerateKey, ParseRoots, VerifyHead, HeadName,
// Namespace, TypeName, ErrUntrusted, ErrUnavailable, ErrConflict,
// MaxDataBytes.
package eventlog

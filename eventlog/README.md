# eventlog

A signed, append-only log of [`event`](../event/README.md) envelopes, published as static files that anyone can read and only one writer can change. One writer appends; any number of readers follow it over HTTP with their own cursor, from a public bucket, a CDN or a local folder.

```text
head.json                            signed: sequence range, segments, latest files, expiry
seg/<first>-<last>-<hash>.ndjson.gz  immutable, gzipped JSON lines of records
latest/<namespace>-<hash>.json       the latest record per type and subject
```

The head is the only object a reader trusts directly. It is signed with Ed25519 and lists every other file with its size and SHA-256, so one signature covers the whole log. Every file except the head is named after its content and never changes, so a CDN can cache it for good. Heads expire, so a frozen mirror is noticed, and each carries a revision, so a reader can refuse a rolled-back head.

## Reading

```go
r := &eventlog.Reader{
    URL:    "https://storage.googleapis.com/some-bucket/v1/",
    Roots:  roots, // eventlog.ParseRoots("id:base64,...")
    Filter: eventlog.Filter{Types: []string{"market.bundle", "data.dataset.released"}},
}
batch, err := r.Next(ctx, cursor) // keep batch.Cursor for next time
if batch.Reset {
    // The log no longer holds what came after the cursor (or the cursor is new):
    // batch.Records are the latest record per type and subject. Replace what you knew.
}
```

A record announces that something changed. When a reader needs the new data, it fetches it from its owner. An unchanged head costs a `304`. `Filter.Match` takes any other predicate, such as a [`trigger`](../trigger/README.md) spec or a [`taxonomy`](../taxonomy/README.md) check, so this package doesn't import either.

## Following

A `Follower` runs the reader for you. It polls on a timer, keeps the cursor in a file, and passes each batch to your handler. The cursor moves only after the handler succeeds, so a failed batch comes round again. Run never polls twice within `MinEvery` (default 30 seconds, the head's cache time), whatever `Every` says and however often `Poke` is called, so a misconfigured or button-mashed reader can't hammer a public log.

```go
f := &eventlog.Follower{
    Reader: r,
    Cursor: eventlog.FileCursor(filepath.Join(stateDir, "events.cursor.json")),
    Every:  2 * time.Minute, // plus up to a fifth more, at random; failures back off
    Handle: func(ctx context.Context, b eventlog.Batch) error { ...; return nil },
}
go f.Run(ctx)   // polls now, then on the timer, until ctx ends
f.Poke()        // poll now, or once MinEvery has passed since the last poll
f.Status()      // last poll, last error, cursor, records seen: for a status line
recent, _, err := r.Tail(ctx, 100) // the newest 100 matching records, for history after a restart
```

## Writing

```go
w := &eventlog.Writer{Store: bucket, Signer: kmsSigner}
result, err := w.Append(ctx, envelopes) // call at least once a day, even with none
```

`Append` validates every envelope and limits its data to `MaxDataBytes`. It skips any envelope whose ID the log already holds. Then it writes a segment and the touched namespaces' latest files, drops segments older than `Retain`, merges segments when there are more than `MaxSegments`, and writes the head last, conditional on the version it read (a GCS generation, an S3 or R2 ETag). If another writer gets there first, `Append` fails with `ErrConflict` and nothing it wrote is referenced. The store's own age-based deletion (a bucket lifecycle rule) can remove old objects, as long as it waits longer than `Retain`. Each head refresh renews the latest files, so they're never removed while in use.

## Implementation status

- [x] Head, segment and latest-file formats, with Ed25519 signatures, digests, expiry and rollback checks
- [x] Writer: append, deduplication by envelope ID, latest files, retention, compaction, conditional head writes
- [x] Reader: cursors, reset to the latest files, filters, `304` reuse, download limits
- [x] Follower: timer, jitter, backoff, poke, status, cursor saved after handling (`FileCursor`); `Reader.Tail` and a segment cache
- [x] Follower: `MinEvery` floor between polls, pokes included
- [x] `eventlogtest`: an in-memory store that serves itself over HTTP, and fixed test keys
- [ ] Store adapters live with their hosts (craggy-event-stream has GCS); none are in the kit

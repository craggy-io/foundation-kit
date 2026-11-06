# feed

An ordered change feed with per-reader cursors. `Hub[T]` stamps each published value with a sequence and time, keeps a bounded backlog, and replays what a reconnecting reader missed. When the gap is too old, or the process restarted, `Subscribe` says so and the reader reloads everything.

```go
h := feed.New[Change](feed.Options{})
h.Publish(Change{Topic: "labs.create"})
entries, complete := h.Subscribe(ctx, lastSeq) // -1 for "from now"
```

Entries announce that something changed, never the new state. Serve a hub over Server-Sent Events with `capability-services/server.Events`.

Consumers: craggy-labs' change feed. craggy-code's workspace feed is the expected second.

## Implementation status

- [x] Publish, Seq, Subscribe with backlog replay and completeness
- [x] Non-blocking fan-out; slow subscribers are dropped
- [ ] Durable cursors across restarts (not needed yet: readers reload on restart)

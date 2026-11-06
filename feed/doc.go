// Package feed is a change feed: a process-wide, ordered log of small change
// announcements that any number of readers follow with their own cursor.
//
// A Hub stamps every published value with the next sequence number and the
// publish time, keeps a bounded backlog, and fans entries out to subscribers
// without blocking the publisher. A subscriber that reconnects with the last
// sequence it saw is replayed what it missed. When that gap is older than the
// backlog, or the cursor is ahead of the hub (the process restarted), Subscribe
// reports the replay as incomplete and the reader must reload everything.
//
// Entries announce that something changed, never the new state: readers re-read
// through their normal API, so the feed cannot drift from the data.
//
// # Invariants
//
//   - Sequences start at 1 and increase by one per Publish.
//   - A subscriber receives entries in sequence order, without gaps, until its
//     channel closes.
//   - Publish never blocks: a subscriber whose buffer is full is dropped and its
//     channel closed, and it reconnects with its last sequence.
//
// # Concurrency
//
// A Hub is safe for concurrent use. Published values are shared with every
// subscriber, so treat them as immutable.
//
// Exported names: Hub, Entry, New, Options.
package feed

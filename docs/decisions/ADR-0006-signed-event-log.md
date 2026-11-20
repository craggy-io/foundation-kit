# ADR-0006: A signed event log published as static files

Status: accepted.

## Context

Backend services need to tell many clients that something changed: a dataset was released, a bundle was published or failed. Examples are craggy-data, the marketplace and craggy-platform, and the clients are craggy-harness installs. The clients are not signed in, there are many of them, and they come and go, so a push channel or a per-client API costs more than the events are worth. The publisher wants one place to write. The readers want to fetch only what's new and to know that it's genuine.

`feed` already has the reading model: a cursor, a bounded backlog, and "reload everything" when the gap is too old. But it lives in one process's memory. `event` already has the fact format.

## Decision

Add `eventlog`, an engine in which:

- **A log is static files.** It has a signed `head.json`, immutable gzipped JSON-lines segments of numbered records, and one latest file per namespace. Any HTTP server can serve it, and a CDN can cache everything but the head for good.
- **One signature covers everything.** The head is signed with Ed25519 under a domain line, and it lists every other file by name, size and SHA-256. Heads expire, so a frozen copy is noticed, and their revisions only rise, so readers refuse rollbacks.
- **The writer owns a `Store` with version preconditions.** A version is the store's own opaque token (a GCS generation, an S3 or R2 ETag). The writer writes the head last, conditional on the version it read, so two writers never both succeed. Every other object is named after its content, so a failed write changes nothing that is published.
- **Size is bounded twice.** Segments are dropped by age (`Retain`) and merged by count (`MaxSegments`). Latest files are bounded by the number of subjects. Data per envelope is capped at `MaxDataBytes`. A reader whose cursor is older than the log gets a reset with the latest files, as `feed` does.
- **Records announce changes; they never carry the new state.** Readers fetch that from its owner.
- **Following is built in.** A `Follower` polls a `Reader` on a timer with jitter and backoff, hands each batch to the host, and saves the cursor (a `FileCursor`) only after the host has handled it. `Reader.Tail` gives the newest records for showing history.
- **It imports only the standard library, `event`, and `fsx`.** `fsx` writes the cursor file atomically; this is the one engine-to-engine dependency, approved here. Filtering by type prefix and subject is built in. Anything richer, such as `trigger` specs or `taxonomy` checks, comes in through `Filter.Match`, so matching needs no other engine.

Hosts own everything Craggy-specific: where events come from, who may publish which types, the schemas of their data, the bucket, and the signing key (in a key-management service, through `Signer`).

## Options considered

- **Extend `feed` with durable storage.** Rejected: `feed` is an in-process hub with subscribers. A published log has no subscribers, and its readers are other machines.
- **Keep the format in the publishing service and give readers a client module there.** Rejected: the harness would then depend on a service repository to read a file format. With the format in the kit, the writer and every reader share one definition.
- **A database or a streaming API in front of the log.** Rejected for now: static files cost nothing per reader beyond egress, and they keep working while the writer is down.

## Consequences

- The harness and any other reader take `eventlog` and a root key, and nothing else.
- Each `Signer` key ID and its public key must be distributed like other root keys. Rotating a key means publishing heads signed by the new key after readers trust it.
- A log's writer must run at least every half `HeadTTL`, or readers start refusing its expired head.

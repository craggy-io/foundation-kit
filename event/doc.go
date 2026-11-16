// Package event defines Envelope, a versioned immutable fact exchanged between
// producers and hosts. Validate checks its structural contract without I/O.
// Hosts establish source, scope and causation; payloads cannot grant authority.
// Callers must not mutate Data or Causation while sharing an envelope.
package event

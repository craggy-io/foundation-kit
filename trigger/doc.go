// Package trigger matches Spec predicates against event envelopes, maps inputs,
// and durably admits opaque Binding targets. Source, Cron, ParseCron and
// ParseDuration provide shared scheduling syntax. Journal serializes mutations
// under an exclusive file lock; reads return detached values. Open acquires the
// lock, Close releases it after callers stop workers. Delivery.RunID survives
// restart and changes only for an explicit retry. Hosts own target execution,
// authorization, schema validation and recovery of external side effects.
package trigger

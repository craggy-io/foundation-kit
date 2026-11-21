# Trigger admission

Construct a `Spec` with an event type, structured predicate, explicit input mappings and optional deduplication paths. Bind it to an opaque target and scope with `Journal.Replace`, then `Publish` facts. Process ready deliveries with compare-and-swap `Transition`; persist the result. The host must resume interrupted work using `Delivery.RunID`, or require an explicit retry if its executor cannot resume safely.

Equality and business keys use `canonjson`, preserving exact integers and treating equivalent decimal forms equally. Event data is normalized with its 8 MiB and depth bounds.

Filters support eq, ends_with, exists, all, any and not. Paths address envelope fields and data properties. Missing mapped fields fail admission visibly. Manual admissions are held until released; replay is opt-in. Clock sources support every and five-field cron, read in UTC or the IANA zone `tz` names; missed occurrences coalesce, and a host may skip a ready delivery (ready → cancelled). Source syntax also describes host-adapted watch, push and change sources.

## Implementation status

- Implemented: matching, mapping, scoped deduplication, causation suppression, clocks, atomic journal, exclusive lock, restart recovery and explicit attempts.
- Host-owned: schema checking, bounded workers, execution, authorization and source adapters.
- Journal uses atomic JSON snapshots; hosts should provision storage for retained event history. No automatic retention or distributed coordinator is provided.

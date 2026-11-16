# ADR-0005: Shared event admission

Status: accepted for the cross-repository trigger implementation.

`event` is a standard-library-only contract. `trigger` is an engine owning structured matching, mapping, deduplication, clocks and durable admissions. It imports `fsx` for atomic persistence and exclusive locks, and `canonjson` for exact JSON equality and stable business keys; this is the approved engine-to-engine dependency. Hosts establish authority and scope, validate producer schemas, and execute opaque targets. The engine contains no workflow, collector, artifact or lab concepts.

Workflow and collection runtimes remain execution authorities. An interrupted admission retains its run identity; only an explicit retry creates a new attempt. History is matched only when replay is explicitly requested. Delivery is durable admission with idempotent publication, not a claim of exactly-once external effects. Clock catch-up coalesces missed occurrences; cron uses UTC.

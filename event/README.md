# Structured events

`Envelope` contains id, versioned type, source, scope, subject, time, causation and object data. Call `Validate` before publication. Producers choose stable IDs; hosts supply trusted source and scope. Consumers must not treat payload values as authority.

## Implementation status

- Implemented: structural validation and JSON envelope.
- Producer schema validation is host-owned.

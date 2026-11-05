# settings

`settings` holds live, user-editable values, declared by the code that reads them. A settings UI can be generated from the declarations.

```go
import "github.com/craggy-io/foundation-kit/settings"
```

Terms used here are defined in [ADR-0001](../docs/decisions/ADR-0001-standard-vocabulary.md). A setting isn't a definition: definitions are authored and shipped in bundles, while settings are tuned by a person at run time.

## Declaring a setting

A host creates one `Store`, and each feature declares its settings on it:

```go
store, err := settings.New(ctx, settings.File("var/settings.json"))
if err != nil {
	return err
}

maxBytes := settings.Int(store, settings.IntSpec[int64]{
	Meta: settings.Meta{
		Key:         "search.strings.max-bytes",
		Title:       "Largest file to search",
		Description: "Larger files are skipped.",
		Group:       "Search",
	},
	Default: 1 << 20, Min: 1, Max: 1 << 30,
	Unit: settings.UnitBytes,
})
```

Declare settings with `Int`, `Bool`, `Choice` or `String`. A malformed declaration or a duplicate key panics, because the first run finds it.

Keys are lower-case with at least two dotted segments, and the first segments name the owner.

## Reading a setting

`Get` returns the live value, without blocking:

```go
limit := maxBytes.Get()
```

A change applies to the next `Get`, with no restart. Read a setting once per operation and keep that value, so that one operation doesn't see two limits.

`Meta.Restart` only tells a UI that a change applies at the next start. The owner has to read that setting once at startup, because `Get` returns a new value as soon as it's set.

## Serving a settings UI

`List` and `Get` return `Info`, which holds everything a UI needs to render a setting. Changes come back as JSON, which `Set` validates against the declaration:

```go
for _, info := range store.List() { /* render it */ }

info, err := store.Set(ctx, "search.strings.max-bytes", json.RawMessage(`2048`))
if errors.Is(err, settings.ErrInvalid) { /* show err to the user */ }

info, err = store.Reset(ctx, "search.strings.max-bytes")
```

## Where a value comes from

A setting's value comes from the first of these that exists, and `Info.Origin` says which:

1. **Stored:** a value set by the user that still fits the declaration.
2. **Environment:** for a string setting, the first variable in `StringSpec.Env` that the host sets. The environment is read once, when the setting is declared.
3. **Default:** the declared default.

If a stored value no longer fits, for example because a limit was lowered, it isn't applied. `Info.StoredError` says why, and the value stays in storage until the user sets or resets that key.

Values stored for keys that nothing has declared are also kept, so that turning a feature off doesn't erase what the user chose for it.

## Secrets

A `StringSpec` with `Secret: true` never serves its value: `Info.Value` and `Info.Default` are empty, and `Info.HasValue` says whether it has one. Errors don't include the value either.

**Secrets aren't encrypted.** Storage holds them in plain text. `File` creates its file readable only by its owner on platforms with Unix permissions. If that isn't enough, leave the value in the environment or provide your own `Storage`.

## Storage

`File` keeps the values as one JSON object and replaces the file atomically. Damaged storage fails `New` rather than silently discarding what the user set. A `Store` assumes it's the only writer, so don't share a file between processes.

To keep the values elsewhere, implement `Storage`, whose `Write` must replace the whole document or leave the old one in place. It's also how a test simulates a failure.

## Implementation status

Already working and tested:

- `New`, `Store`, `File` and `Storage`
- `Int`, `Bool`, `Choice` and `String`
- `List`, `Get`, `Set` and `Reset`

Extracted from `cclab-platform`'s `core/settings`, which hasn't moved to this package yet. It will need these changes when it does:

1. [ ] Rename `Open(path)` to `New(ctx, File(path))`, and pass `ctx` to `Set` and `Reset`.
2. [ ] Rename `Kind`, `Source`, `Option`, `Overridden` and `Problem` to `Type`, `Origin`, `ChoiceOption`, `Stored` and `StoredError`. Their JSON names change too, so the UI has to change with them.
3. [ ] Read `Info.Value` and `Info.Default` as JSON rather than `any`.
4. [ ] Add `jsonschema` descriptions in the adapter that exposes `Info`, because this package has no such tags.

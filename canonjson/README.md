# canonjson

`canonjson` renders JSON in one canonical byte form, so that equal values hash equally however they were written.

```go
import "github.com/craggy-io/foundation-kit/canonjson"
```

It uses only the standard library.

```go
b, err := canonjson.Normalize([]byte(`{"b": 1.0, "a": [1e2]}`))
// b is {"a":[100],"b":1}

b, err = canonjson.Marshal(record) // encoding/json, then Normalize
sum := sha256.Sum256(b)
```

## The form

- Compact, with object members sorted by key in bytewise UTF-8 order.
- Numbers as exact decimals: no exponent, no trailing zeros, no plus sign, and `-0` as `0`. `1`, `1.0`, `1e0` and `10e-1` are all `1`, and large or precise numbers keep every digit.
- Strings escaped as `encoding/json` escapes them, including `<`, `>` and `&`.

This is **not** RFC 8785 (JCS), which writes numbers as doubles and sorts keys by UTF-16 code units. It is byte-for-byte compatible with artifact-services' v1 canonical JSON, checked by running both on more than 20,000 generated documents.

Digests made from this form are stored by consumers, so the form never changes. A different form would be a new package.

## What it rejects

- `ErrInvalid`: a syntax error, trailing content, invalid UTF-8 or a duplicate object member.
- `ErrLimit`: input or output over `MaxBytes` (8 MiB), nesting deeper than `MaxDepth` (64), or a number whose exact form would be huge, such as `1e999999999`.

## Implementation status

- Working: `Normalize`, `Marshal`.

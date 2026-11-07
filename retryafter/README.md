# retryafter

`retryafter` reads the HTTP `Retry-After` header, which servers send with 429 and 503 responses.

```go
import "github.com/craggy-io/foundation-kit/retryafter"
```

It uses only the standard library and doesn't import `net/http`.

```go
if wait, ok := retryafter.Delay(resp.Header.Get("Retry-After"), time.Now()); ok {
	backoff = wait
}

at, ok := retryafter.Parse(resp.Header.Get("Retry-After"), time.Now()) // an absolute time
```

Both forms of the header are accepted: a number of seconds, and an HTTP date in the current format or either obsolete one. A date in the past means retry now, so the delay is never negative. `ok` is false for an empty or unreadable value, and the caller should fall back to its own backoff.

Non-standard headers such as `X-RateLimit-Reset` differ between services, so they're left to the client that knows them.

## Implementation status

- Working: `Parse`, `Delay`.

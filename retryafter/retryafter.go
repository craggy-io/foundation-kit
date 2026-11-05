// Package retryafter interprets the HTTP Retry-After header (RFC 9110, section
// 10.2.3), which a server sends with 429 and 503 responses to say when to try again.
//
// The header holds either a number of seconds or an HTTP date. Both forms are
// accepted, along with the obsolete date formats that HTTP/1.1 recipients must still
// read. This package uses only the standard library and doesn't import net/http.
//
// # Concurrency
//
// Parse and Delay are pure functions and safe for concurrent use. Pass now
// explicitly, so tests needn't wait for real time.
//
// # Exported names
//
//	Parse      the time to retry at
//	Delay      how long to wait from now
package retryafter

import (
	"strconv"
	"strings"
	"time"
)

// maxSeconds keeps the delay within time.Duration.
const maxSeconds = int64(1<<63-1) / int64(time.Second)

// HTTP-date formats, preferred first (RFC 9110, section 5.6.7).
var dateFormats = []string{
	"Mon, 02 Jan 2006 15:04:05 GMT", // IMF-fixdate
	time.RFC850,                     // obsolete RFC 850 form
	time.ANSIC,                      // obsolete asctime form
}

// Parse returns the time at which to retry, given a Retry-After value and the
// current time. Pass the header's value as it is, for example from
// resp.Header.Get("Retry-After").
//
// ok is false when the value is empty or unreadable, and the caller should use its
// own backoff. A date already in the past gives now, so the result is never earlier
// than now. A delay too large for time.Duration is capped.
func Parse(value string, now time.Time) (at time.Time, ok bool) {
	d, ok := Delay(value, now)
	if !ok {
		return time.Time{}, false
	}
	return now.Add(d), true
}

// Delay returns how long to wait from now before retrying. It's never negative. ok
// is false when the value is empty or unreadable. See Parse.
func Delay(value string, now time.Time) (d time.Duration, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if isDigits(value) {
		secs, err := strconv.ParseInt(value, 10, 64)
		if err != nil || secs > maxSeconds { // too many digits for int64
			secs = maxSeconds
		}
		return time.Duration(secs) * time.Second, true
	}
	for _, layout := range dateFormats {
		t, err := time.Parse(layout, value)
		if err != nil {
			continue
		}
		if d := t.Sub(now); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

// isDigits reports whether s is delay-seconds: one or more ASCII digits, with no
// sign.
func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

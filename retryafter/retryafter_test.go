package retryafter_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/craggy-io/foundation-kit/retryafter"
)

var now = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func TestDelay(t *testing.T) {
	tests := []struct {
		value string
		want  time.Duration
		ok    bool
	}{
		{"", 0, false},
		{"   ", 0, false},
		{"0", 0, true},
		{"120", 2 * time.Minute, true},
		{" 7 ", 7 * time.Second, true},
		{"-5", 0, false},
		{"+5", 0, false},
		{"1.5", 0, false},
		{"soon", 0, false},
		{"99999999999999999999999", time.Duration(math.MaxInt64 / int64(time.Second) * int64(time.Second)), true},
		{"Thu, 05 Nov 2026 12:01:30 GMT", 90 * time.Second, true},
		{"Thursday, 05-Nov-26 12:00:10 GMT", 10 * time.Second, true},
		{"Thu Nov  5 12:00:03 2026", 3 * time.Second, true},
		{"Thu, 05 Nov 2026 11:00:00 GMT", 0, true}, // past: retry now
		{"Thu, 05 Nov 2026 12:01:30 PST", 0, false},
	}
	for _, tt := range tests {
		d, ok := retryafter.Delay(tt.value, now)
		if d != tt.want || ok != tt.ok {
			t.Errorf("Delay(%q) = %v, %v; want %v, %v", tt.value, d, ok, tt.want, tt.ok)
		}
	}
}

func TestParse(t *testing.T) {
	at, ok := retryafter.Parse("30", now)
	if !ok || !at.Equal(now.Add(30*time.Second)) {
		t.Fatalf("Parse(30) = %v, %v", at, ok)
	}
	if _, ok := retryafter.Parse("never", now); ok {
		t.Fatal("Parse accepted an unreadable value")
	}
}

func ExampleDelay() {
	// In a client: wait, ok := retryafter.Delay(resp.Header.Get("Retry-After"), time.Now())
	wait, ok := retryafter.Delay("120", now)
	fmt.Println(wait, ok)
	// Output: 2m0s true
}

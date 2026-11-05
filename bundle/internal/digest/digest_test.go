package digest

import "testing"

// This is a golden test: digests are persisted, so this value must never change.
func TestSum(t *testing.T) {
	h := New()
	h.Write([]byte("hel"))
	h.Write([]byte("lo"))
	want := "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got := Sum(h); got != want {
		t.Errorf("Sum = %s, want %s", got, want)
	}
}

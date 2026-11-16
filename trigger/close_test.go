package trigger_test

import (
	"errors"
	"github.com/craggy-io/foundation-kit/trigger"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClosedJournalCannotWriteAfterUnlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	old, err := trigger.Open(t.Context(), path)
	requireSuccess(t, err)
	requireSuccess(t, old.Close())
	current, err := trigger.Open(t.Context(), path)
	requireSuccess(t, err)
	defer current.Close()
	if err := old.Replace(t.Context(), nil); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed replace: %v", err)
	}
	if err := old.Tick(t.Context(), time.Now()); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed tick: %v", err)
	}
	if err := old.Transition(t.Context(), "", "missing", "ready", "running", "", false); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed mutation: %v", err)
	}
}

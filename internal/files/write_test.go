package files

import (
	"testing"
	"time"
)

func TestConflictPath(t *testing.T) {
	ts := time.Date(2026, 6, 6, 15, 30, 0, 0, time.UTC)
	got := ConflictPath("notes/tasks.org", ts)
	want := "notes/tasks.conflict-20260606-153000.org"
	if got != want {
		t.Fatalf("ConflictPath() = %q, want %q", got, want)
	}
}

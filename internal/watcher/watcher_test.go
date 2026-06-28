package watcher

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/yuzumone/org-syncd/internal/files"
)

func TestEventFromFSNotify(t *testing.T) {
	w := New("/tmp/org", files.NewMatcher([]string{".org"}, nil), time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tests := []struct {
		name string
		op   fsnotify.Op
		want Event
		ok   bool
	}{
		{name: "write", op: fsnotify.Write, want: Event{Path: "tasks.org", Op: OpWrite}, ok: true},
		{name: "create", op: fsnotify.Create, want: Event{Path: "tasks.org", Op: OpCreate}, ok: true},
		{name: "remove", op: fsnotify.Remove, want: Event{Path: "tasks.org", Op: OpDelete}, ok: true},
		{name: "rename", op: fsnotify.Rename, want: Event{Path: "tasks.org", Op: OpDelete}, ok: true},
		{name: "directory remove", op: fsnotify.Remove, want: Event{Path: "website", Op: OpDelete}, ok: true},
		{name: "ignored extension", op: fsnotify.Write, want: Event{}, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := "/tmp/org/tasks.org"
			switch tt.name {
			case "ignored extension":
				name = "/tmp/org/tasks.tmp"
			case "directory remove":
				name = "/tmp/org/website"
			}
			got, ok := w.eventFromFSNotify(fsnotify.Event{Name: name, Op: tt.op})
			if ok != tt.ok || got != tt.want {
				t.Fatalf("eventFromFSNotify = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestEventsFromPendingSortsByPath(t *testing.T) {
	got := eventsFromPending(map[string]Event{
		"b.org": {Path: "b.org", Op: OpWrite},
		"a.org": {Path: "a.org", Op: OpDelete},
	})
	if len(got) != 2 || got[0].Path != "a.org" || got[1].Path != "b.org" {
		t.Fatalf("eventsFromPending = %+v", got)
	}
}

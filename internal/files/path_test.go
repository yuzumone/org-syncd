package files

import "testing"

func TestNormalizeRelativePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{name: "relative", path: "tasks.org", want: "tasks.org"},
		{name: "clean", path: "projects/../tasks.org", want: "tasks.org"},
		{name: "nested", path: "notes/inbox.md", want: "notes/inbox.md"},
		{name: "escape", path: "../secret.org", wantErr: true},
		{name: "empty", path: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeRelativePath("/tmp/org", tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDocID(t *testing.T) {
	got := DocID("notes/tasks.org")
	want := "file:notes/tasks.org"
	if got != want {
		t.Fatalf("DocID() = %q, want %q", got, want)
	}
}

package files

import "testing"

func TestMatcherInclude(t *testing.T) {
	m := NewMatcher([]string{".org", ".md", ".txt"}, []string{".git", ".DS_Store", "*.tmp"})
	tests := []struct {
		path string
		want bool
	}{
		{path: "tasks.org", want: true},
		{path: "notes/readme.md", want: true},
		{path: "notes/todo.txt", want: true},
		{path: "notes/tmp.tmp", want: false},
		{path: "notes/image.png", want: false},
		{path: ".hidden.org", want: false},
		{path: "notes/.hidden.org", want: false},
		{path: "notes/.DS_Store", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := m.Include(tt.path); got != tt.want {
				t.Fatalf("Include(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

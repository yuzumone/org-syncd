package orgvault

import "time"

type Note struct {
	Path       string    `json:"path"`
	Name       string    `json:"name,omitempty"`
	Title      string    `json:"title,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	ModifiedAt time.Time `json:"modified_at"`
	Size       int64     `json:"size"`
	Content    string    `json:"content,omitempty"`
}

type Folder struct {
	Path      string `json:"path"`
	NoteCount int    `json:"note_count"`
}

type WriteResult struct {
	Path       string    `json:"path"`
	Created    bool      `json:"created"`
	ModifiedAt time.Time `json:"modified_at"`
}

type ListOptions struct {
	Folder        string
	Name          string
	Tag           string
	ModifiedAfter time.Time
	Sort          string
	Order         string
	Limit         int
}

type SearchOptions struct {
	Query  string
	Folder string
	Limit  int
}

type Match struct {
	Path    string   `json:"path"`
	Title   string   `json:"title,omitempty"`
	Line    int      `json:"line"`
	Text    string   `json:"text"`
	Context []string `json:"context"`
}

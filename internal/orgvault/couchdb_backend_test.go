package orgvault

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/yuzumone/org-syncd/internal/couchdb"
)

func TestCouchDBBackendWriteNoteUsesLatestRev(t *testing.T) {
	var put couchdb.FileDoc
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + docIDFromRequest(t, r) {
		case "PUT ":
			w.WriteHeader(http.StatusCreated)
		case "GET file:inbox.org":
			writeJSON(t, w, couchdb.FileDoc{
				ID:      "file:inbox.org",
				Rev:     "1-old",
				Type:    "file",
				Path:    "inbox.org",
				Content: "old\n",
			})
		case "PUT file:inbox.org":
			if err := json.NewDecoder(r.Body).Decode(&put); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusCreated)
			writeJSON(t, w, map[string]string{"ok": "true", "rev": "2-new"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client, err := couchdb.New(server.URL, "orgsync", "", "")
	if err != nil {
		t.Fatal(err)
	}
	backend := NewCouchDBBackend(client, "mcp-test")
	got, err := backend.WriteNote("inbox.org", "new\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.Created {
		t.Fatal("expected existing document update")
	}
	if put.Rev != "1-old" {
		t.Fatalf("put _rev = %q, want 1-old", put.Rev)
	}
	if put.UpdatedBy != "mcp-test" {
		t.Fatalf("updated_by = %q, want mcp-test", put.UpdatedBy)
	}
	if put.Content != "new\n" || put.ContentSHA256 == "" {
		t.Fatalf("unexpected doc: %#v", put)
	}
}

func TestCouchDBBackendAppendRetriesConflictOnce(t *testing.T) {
	gets := 0
	puts := 0
	var final couchdb.FileDoc
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + docIDFromRequest(t, r) {
		case "PUT ":
			w.WriteHeader(http.StatusCreated)
		case "GET file:inbox.org":
			gets++
			if gets == 1 {
				writeJSON(t, w, couchdb.FileDoc{
					ID:      "file:inbox.org",
					Rev:     "1-old",
					Type:    "file",
					Path:    "inbox.org",
					Content: "old\n",
				})
				return
			}
			writeJSON(t, w, couchdb.FileDoc{
				ID:      "file:inbox.org",
				Rev:     "2-remote",
				Type:    "file",
				Path:    "inbox.org",
				Content: "remote\n",
			})
		case "PUT file:inbox.org":
			puts++
			if puts == 1 {
				w.WriteHeader(http.StatusConflict)
				writeJSON(t, w, map[string]string{"error": "conflict"})
				return
			}
			if err := json.NewDecoder(r.Body).Decode(&final); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusCreated)
			writeJSON(t, w, map[string]string{"ok": "true", "rev": "3-new"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client, err := couchdb.New(server.URL, "orgsync", "", "")
	if err != nil {
		t.Fatal(err)
	}
	backend := NewCouchDBBackend(client, "mcp-test")
	if _, err := backend.AppendNote("inbox.org", "append\n"); err != nil {
		t.Fatal(err)
	}
	if gets != 2 || puts != 2 {
		t.Fatalf("gets=%d puts=%d, want 2/2", gets, puts)
	}
	if final.Rev != "2-remote" {
		t.Fatalf("final _rev = %q, want 2-remote", final.Rev)
	}
	if final.Content != "remote\nappend\n" {
		t.Fatalf("final content = %q", final.Content)
	}
}

func docIDFromRequest(t *testing.T, r *http.Request) string {
	t.Helper()
	path := strings.TrimPrefix(r.URL.Path, "/orgsync")
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return ""
	}
	id, err := url.PathUnescape(path)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}

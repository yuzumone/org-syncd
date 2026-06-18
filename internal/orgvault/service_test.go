package orgvault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadNoteRejectsTraversal(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReadNote("../secret.org"); err == nil {
		t.Fatal("expected traversal error")
	}
}

func TestWriteNoteCreatesBackupAndReplaces(t *testing.T) {
	root := t.TempDir()
	svc, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.WriteNote("inbox.org", "#+title: Inbox\nold\n"); err != nil {
		t.Fatal(err)
	}
	got, err := svc.WriteNote("inbox.org", "#+title: Inbox\nnew\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.Created {
		t.Fatal("expected replacement")
	}
	if got.BackupPath == "" {
		t.Fatal("expected backup path")
	}
	backup, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(got.BackupPath)))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != "#+title: Inbox\nold\n" {
		t.Fatalf("backup = %q", string(backup))
	}
	note, err := svc.ReadNote("inbox.org")
	if err != nil {
		t.Fatal(err)
	}
	if note.Content != "#+title: Inbox\nnew\n" {
		t.Fatalf("content = %q", note.Content)
	}
}

func TestAppendNoteCreatesFile(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.AppendNote("inbox.org", "* TODO write\n")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Created {
		t.Fatal("expected created")
	}
	note, err := svc.ReadNote("inbox.org")
	if err != nil {
		t.Fatal(err)
	}
	if note.Content != "* TODO write\n" {
		t.Fatalf("content = %q", note.Content)
	}
}

func TestListFoldersAndNotesSkipBackupAndHidden(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "inbox.org", "#+title: Inbox\n")
	mustWrite(t, root, "roam/personal-os.org", "#+title: Personal OS\n* Task :work:\n")
	mustWrite(t, root, ".backup/20260618120000/old.org", "old\n")
	mustWrite(t, root, ".hidden.org", "hidden\n")

	svc, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	folders, err := svc.ListFolders()
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 2 {
		t.Fatalf("folders = %#v", folders)
	}
	notes, err := svc.ListNotes(ListOptions{Folder: "roam", Tag: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Path != "roam/personal-os.org" || notes[0].Title != "Personal OS" {
		t.Fatalf("notes = %#v", notes)
	}
}

func TestSearchNotes(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "roam/org-roam.org", "#+title: Org Roam\nbefore\norg-roam notes\nafter\n")
	svc, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := svc.SearchNotes(SearchOptions{Query: "ORG-ROAM", Folder: "roam", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %#v", matches)
	}
	if matches[0].Line != 3 || matches[0].Title != "Org Roam" || len(matches[0].Context) != 3 {
		t.Fatalf("match = %#v", matches[0])
	}
}

func mustWrite(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

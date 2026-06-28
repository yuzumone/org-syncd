package couchdb

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDocRequestEscapesSlashOnce(t *testing.T) {
	client, err := New("http://couchdb.example", "orgsync", "", "")
	if err != nil {
		t.Fatal(err)
	}
	req, err := client.newDocRequest(context.Background(), http.MethodGet, "file:roam/20250427120847-revisiting_android_spinners.org", nil)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(req.URL.String(), "%252F") {
		t.Fatalf("doc URL double-escaped slash: %s", req.URL.String())
	}
	if !strings.Contains(req.URL.String(), "file:roam%2F20250427120847-revisiting_android_spinners.org") {
		t.Fatalf("doc URL did not escape slash once: %s", req.URL.String())
	}
	if req.URL.Path != "/orgsync/file:roam/20250427120847-revisiting_android_spinners.org" {
		t.Fatalf("decoded path = %q", req.URL.Path)
	}
}

func TestRegularRequestClearsRawPath(t *testing.T) {
	client, err := New("http://couchdb.example/prefix%2Fraw", "orgsync", "", "")
	if err != nil {
		t.Fatal(err)
	}
	req, err := client.newRequest(context.Background(), http.MethodGet, "orgsync/_all_docs?include_docs=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.RawPath != "" {
		t.Fatalf("RawPath = %q", req.URL.RawPath)
	}
}

func TestNewDocRequestSetsBasicAuth(t *testing.T) {
	client, err := New("http://couchdb.example", "orgsync", "user", "pass")
	if err != nil {
		t.Fatal(err)
	}
	req, err := client.newDocRequest(context.Background(), http.MethodPut, "file:tasks.org", io.Reader(nil))
	if err != nil {
		t.Fatal(err)
	}
	gotUser, gotPass, ok := req.BasicAuth()
	if !ok || gotUser != "user" || gotPass != "pass" {
		t.Fatalf("BasicAuth = %q/%q ok=%v", gotUser, gotPass, ok)
	}
}

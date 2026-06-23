package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yuzumone/org-syncd/internal/org"
)

func TestHTTPHandlerRequiresBearerToken(t *testing.T) {
	handler := HTTPHandler(nil, newTestOAuthProvider(t))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

func TestHTTPHandlerProcessesJSONRPC(t *testing.T) {
	handler := HTTPHandler(nil, newTestOAuthProvider(t))
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", res.Code, http.StatusOK, res.Body.String())
	}
	var got response
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Error != nil || got.Result == nil {
		t.Fatalf("unexpected response: %+v", got)
	}
	result := got.Result.(map[string]any)
	if result["protocolVersion"] != "2025-06-18" {
		t.Fatalf("protocolVersion = %v", result["protocolVersion"])
	}
}

func TestHTTPHandlerRejectsUnsupportedProtocolVersion(t *testing.T) {
	handler := HTTPHandler(nil, newTestOAuthProvider(t))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "unsupported")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}

func TestHTTPHandlerAcceptsNotification(t *testing.T) {
	handler := HTTPHandler(nil, newTestOAuthProvider(t))
	body := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusAccepted)
	}
}

func TestHTTPHandlerRejectsOrigin(t *testing.T) {
	handler := HTTPHandler(nil, newTestOAuthProvider(t))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://example.com")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusForbidden)
	}
}

func TestHTTPHandlerAppendsFile(t *testing.T) {
	vault := &appendVault{}
	handler := HTTPHandler(vault, newTestOAuthProvider(t))
	req := httptest.NewRequest(http.MethodPost, FilesAppendPath, strings.NewReader(`{"path":"inbox.org","content":"hello\n"}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", res.Code, http.StatusOK, res.Body.String())
	}
	if vault.path != "inbox.org" || vault.content != "hello\n" {
		t.Fatalf("append args = path %q content %q", vault.path, vault.content)
	}
	var got org.WriteResult
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Path != "inbox.org" {
		t.Fatalf("path = %q, want inbox.org", got.Path)
	}
}

func TestHTTPHandlerFilesAppendRequiresBearerToken(t *testing.T) {
	handler := HTTPHandler(&appendVault{}, newTestOAuthProvider(t))
	req := httptest.NewRequest(http.MethodPost, FilesAppendPath, strings.NewReader(`{"path":"inbox.org","content":"hello\n"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

func TestHTTPHandlerFilesAppendRejectsBadRequest(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
	}{
		{name: "content type", contentType: "text/plain", body: `{"path":"inbox.org","content":"hello\n"}`, wantStatus: http.StatusUnsupportedMediaType},
		{name: "missing path", contentType: "application/json", body: `{"content":"hello\n"}`, wantStatus: http.StatusBadRequest},
		{name: "missing content", contentType: "application/json", body: `{"path":"inbox.org"}`, wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := HTTPHandler(&appendVault{}, newTestOAuthProvider(t))
			req := httptest.NewRequest(http.MethodPost, FilesAppendPath, strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer secret")
			req.Header.Set("Content-Type", tt.contentType)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", res.Code, tt.wantStatus, res.Body.String())
			}
		})
	}
}

func newTestOAuthProvider(t *testing.T) *OAuthProvider {
	t.Helper()
	provider, err := NewOAuthProvider(OAuthConfig{
		BaseURL:    "http://localhost:8080",
		Password:   "secret",
		DataDir:    t.TempDir(),
		RefreshTTL: 14 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

type appendVault struct {
	path    string
	content string
}

func (v *appendVault) ReadNote(string) (org.Note, error) {
	return org.Note{}, fmt.Errorf("not implemented")
}

func (v *appendVault) WriteNote(string, string) (org.WriteResult, error) {
	return org.WriteResult{}, fmt.Errorf("not implemented")
}

func (v *appendVault) AppendNote(path, content string) (org.WriteResult, error) {
	v.path = path
	v.content = content
	return org.WriteResult{Path: path, ModifiedAt: time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)}, nil
}

func (v *appendVault) ListFolders() ([]org.Folder, error) {
	return nil, fmt.Errorf("not implemented")
}

func (v *appendVault) ListNotes(org.ListOptions) ([]org.Note, error) {
	return nil, fmt.Errorf("not implemented")
}

func (v *appendVault) SearchNotes(org.SearchOptions) ([]org.Match, error) {
	return nil, fmt.Errorf("not implemented")
}

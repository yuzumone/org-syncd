package mcpserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestOAuthFlowAndPersistence(t *testing.T) {
	dataDir := t.TempDir()
	provider := newOAuthProviderForDir(t, dataDir)
	handler := HTTPHandler(nil, provider)

	unauthorized := performRequest(handler, http.MethodPost, EndpointPath, `{}`, map[string]string{
		"Content-Type": "application/json",
	})
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	if got := unauthorized.Header().Get("WWW-Authenticate"); !strings.Contains(got, "resource_metadata=") {
		t.Fatalf("WWW-Authenticate = %q", got)
	}

	metadata := performRequest(handler, http.MethodGet, protectedResourceMetadataPath, "", nil)
	if metadata.Code != http.StatusOK {
		t.Fatalf("metadata status = %d", metadata.Code)
	}

	registration := performRequest(handler, http.MethodPost, registerPath,
		`{"redirect_uris":["http://127.0.0.1:9911/callback"],"client_name":"test client","grant_types":["authorization_code","refresh_token"]}`,
		map[string]string{"Content-Type": "application/json"})
	if registration.Code != http.StatusCreated {
		t.Fatalf("registration status = %d: %s", registration.Code, registration.Body.String())
	}
	var client struct {
		ID     string `json:"client_id"`
		Secret string `json:"client_secret"`
	}
	decodeRecorderJSON(t, registration, &client)

	verifier := "test-code-verifier-with-enough-entropy-1234567890"
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	authorizeURL := authorizePath + "?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ID},
		"redirect_uri":          {"http://127.0.0.1:9911/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"resource":              {"http://localhost:8080/mcp"},
		"scope":                 {"mcp"},
		"state":                 {"test-state"},
	}.Encode()
	authorize := performRequest(handler, http.MethodGet, authorizeURL, "", nil)
	if authorize.Code != http.StatusOK {
		t.Fatalf("authorize status = %d: %s", authorize.Code, authorize.Body.String())
	}
	code := hiddenValue(t, authorize.Body.String(), "code")
	csrf := hiddenValue(t, authorize.Body.String(), "csrf")

	beforeApproval := exchangeCode(handler, client.ID, client.Secret, code, verifier)
	if beforeApproval.Code != http.StatusBadRequest {
		t.Fatalf("unapproved code status = %d", beforeApproval.Code)
	}
	wrongApproval := performForm(handler, approvePath, url.Values{
		"code": {code}, "csrf": {csrf}, "password": {"wrong"},
	})
	if wrongApproval.Code != http.StatusUnauthorized {
		t.Fatalf("wrong approval status = %d", wrongApproval.Code)
	}
	csrf = hiddenValue(t, wrongApproval.Body.String(), "csrf")

	approval := performForm(handler, approvePath, url.Values{
		"code":     {code},
		"csrf":     {csrf},
		"password": {"secret"},
	})
	if approval.Code != http.StatusFound {
		t.Fatalf("approval status = %d: %s", approval.Code, approval.Body.String())
	}
	location, err := url.Parse(approval.Header().Get("Location"))
	if err != nil || location.Query().Get("state") != "test-state" || location.Query().Get("code") != code {
		t.Fatalf("approval redirect = %q", approval.Header().Get("Location"))
	}

	tokenResponse := exchangeCode(handler, client.ID, client.Secret, code, verifier)
	if tokenResponse.Code != http.StatusOK {
		t.Fatalf("token status = %d: %s", tokenResponse.Code, tokenResponse.Body.String())
	}
	var tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	decodeRecorderJSON(t, tokenResponse, &tokens)
	assertMCPAccess(t, handler, tokens.AccessToken, http.StatusOK)

	refreshResponse := performForm(handler, tokenPath, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {client.ID},
		"client_secret": {client.Secret},
		"resource":      {"http://localhost:8080/mcp"},
	})
	if refreshResponse.Code != http.StatusOK {
		t.Fatalf("refresh status = %d: %s", refreshResponse.Code, refreshResponse.Body.String())
	}
	var refreshed struct {
		AccessToken string `json:"access_token"`
	}
	decodeRecorderJSON(t, refreshResponse, &refreshed)
	assertMCPAccess(t, handler, tokens.AccessToken, http.StatusUnauthorized)
	assertMCPAccess(t, handler, refreshed.AccessToken, http.StatusOK)

	reloaded := newOAuthProviderForDir(t, dataDir)
	assertMCPAccess(t, HTTPHandler(nil, reloaded), refreshed.AccessToken, http.StatusOK)

	statePath := filepath.Join(dataDir, "oauth-state.json")
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), client.Secret) || strings.Contains(string(state), refreshed.AccessToken) {
		t.Fatal("OAuth state contains a plaintext credential")
	}
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("OAuth state mode = %o", info.Mode().Perm())
	}
}

func TestOAuthRejectsUnsafeRedirectURI(t *testing.T) {
	handler := HTTPHandler(nil, newTestOAuthProvider(t))
	response := performRequest(handler, http.MethodPost, registerPath,
		`{"redirect_uris":["https://safe.example/callback#fragment"]}`,
		map[string]string{"Content-Type": "application/json"})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestOAuthRequiresHTTPSOutsideLocalhost(t *testing.T) {
	_, err := NewOAuthProvider(OAuthConfig{
		BaseURL: "http://example.com", Password: "secret", DataDir: t.TempDir(), RefreshTTL: time.Hour,
	})
	if err == nil {
		t.Fatal("expected insecure BASE_URL error")
	}
}

func newOAuthProviderForDir(t *testing.T, dataDir string) *OAuthProvider {
	t.Helper()
	provider, err := NewOAuthProvider(OAuthConfig{
		BaseURL:    "http://localhost:8080",
		Password:   "secret",
		DataDir:    dataDir,
		RefreshTTL: 14 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func exchangeCode(handler http.Handler, clientID, clientSecret, code, verifier string) *httptest.ResponseRecorder {
	return performForm(handler, tokenPath, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {"http://127.0.0.1:9911/callback"},
		"code_verifier": {verifier},
		"resource":      {"http://localhost:8080/mcp"},
	})
}

func assertMCPAccess(t *testing.T, handler http.Handler, token string, want int) {
	t.Helper()
	response := performRequest(handler, http.MethodPost, EndpointPath,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + token})
	if response.Code != want {
		t.Fatalf("MCP access status = %d, want %d: %s", response.Code, want, response.Body.String())
	}
}

func performForm(handler http.Handler, path string, values url.Values) *httptest.ResponseRecorder {
	return performRequest(handler, http.MethodPost, path, values.Encode(), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	})
}

func performRequest(handler http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeRecorderJSON(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func hiddenValue(t *testing.T, body, name string) string {
	t.Helper()
	pattern := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `" value="([^"]+)"`)
	match := pattern.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("hidden input %q not found", name)
	}
	return match[1]
}

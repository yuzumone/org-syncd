package server

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	protectedResourceMetadataPath = "/.well-known/oauth-protected-resource"
	authorizationMetadataPath     = "/.well-known/oauth-authorization-server"
	registerPath                  = "/oauth/register"
	authorizePath                 = "/oauth/authorize"
	approvePath                   = "/oauth/approve"
	tokenPath                     = "/oauth/token"

	accessTokenTTL = time.Hour
	pendingTTL     = 10 * time.Minute
	clientTTL      = 24 * time.Hour
	maxClients     = 100
	maxPending     = 100
	maxOAuthBody   = 1 << 20
)

type OAuthConfig struct {
	BaseURL    string
	Password   string
	DataDir    string
	RefreshTTL time.Duration
}

type OAuthProvider struct {
	baseURL     string
	resourceURL string
	password    string
	statePath   string
	refreshTTL  time.Duration

	mu            sync.Mutex
	saveMu        sync.Mutex
	clients       map[string]registeredClient
	pending       map[string]pendingAuthorization
	accessTokens  map[string]tokenRecord
	refreshTokens map[string]tokenRecord
	failed        int
	lockouts      int
	lockedUntil   time.Time
}

type registeredClient struct {
	ID              string    `json:"id"`
	SecretHash      string    `json:"secret_hash,omitempty"`
	RedirectURIs    []string  `json:"redirect_uris"`
	Name            string    `json:"name,omitempty"`
	TokenAuthMethod string    `json:"token_auth_method"`
	CreatedAt       time.Time `json:"created_at"`
}

type pendingAuthorization struct {
	ClientID      string
	RedirectURI   string
	CodeChallenge string
	State         string
	Resource      string
	CSRF          string
	CreatedAt     time.Time
	Approved      bool
}

type tokenRecord struct {
	ClientID       string    `json:"client_id"`
	Resource       string    `json:"resource"`
	ExpiresAt      time.Time `json:"expires_at"`
	AccessTokenKey string    `json:"access_token_key,omitempty"`
}

type persistedOAuthState struct {
	Clients       map[string]registeredClient `json:"clients"`
	AccessTokens  map[string]tokenRecord      `json:"access_tokens"`
	RefreshTokens map[string]tokenRecord      `json:"refresh_tokens"`
}

func NewOAuthProvider(cfg OAuthConfig) (*OAuthProvider, error) {
	baseURL, err := normalizeBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	if cfg.Password == "" {
		return nil, fmt.Errorf("MCP_AUTH_TOKEN is required")
	}
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("DATA_DIR is required")
	}
	if cfg.RefreshTTL <= 0 {
		return nil, fmt.Errorf("refresh token lifetime must be positive")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create DATA_DIR: %w", err)
	}
	if err := os.Chmod(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("secure DATA_DIR: %w", err)
	}

	p := &OAuthProvider{
		baseURL:       baseURL,
		resourceURL:   baseURL + EndpointPath,
		password:      cfg.Password,
		statePath:     filepath.Join(cfg.DataDir, "oauth-state.json"),
		refreshTTL:    cfg.RefreshTTL,
		clients:       make(map[string]registeredClient),
		pending:       make(map[string]pendingAuthorization),
		accessTokens:  make(map[string]tokenRecord),
		refreshTokens: make(map[string]tokenRecord),
	}
	if err := p.load(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *OAuthProvider) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+protectedResourceMetadataPath, p.protectedResourceMetadata)
	mux.HandleFunc("GET "+protectedResourceMetadataPath+EndpointPath, p.protectedResourceMetadata)
	mux.HandleFunc("GET "+authorizationMetadataPath, p.authorizationMetadata)
	mux.HandleFunc("POST "+registerPath, p.register)
	mux.HandleFunc("GET "+authorizePath, p.authorize)
	mux.HandleFunc("POST "+approvePath, p.approve)
	mux.HandleFunc("POST "+tokenPath, p.token)
}

func (p *OAuthProvider) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		if p.validStaticToken(authorization) || p.validAccessToken(authorization) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q`, p.baseURL+protectedResourceMetadataPath))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

func (p *OAuthProvider) Save() error {
	p.saveMu.Lock()
	defer p.saveMu.Unlock()

	p.mu.Lock()
	p.cleanupLocked(time.Now())
	state := persistedOAuthState{
		Clients:       cloneMap(p.clients),
		AccessTokens:  cloneMap(p.accessTokens),
		RefreshTokens: cloneMap(p.refreshTokens),
	}
	p.mu.Unlock()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode OAuth state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(p.statePath), ".oauth-state-*")
	if err != nil {
		return fmt.Errorf("create OAuth state temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure OAuth state temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write OAuth state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync OAuth state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close OAuth state: %w", err)
	}
	if err := os.Rename(tmpName, p.statePath); err != nil {
		return fmt.Errorf("replace OAuth state: %w", err)
	}
	return nil
}

func (p *OAuthProvider) protectedResourceMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":              p.resourceURL,
		"authorization_servers": []string{p.baseURL},
		"scopes_supported":      []string{"mcp"},
	})
}

func (p *OAuthProvider) authorizationMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                p.baseURL,
		"authorization_endpoint":                p.baseURL + authorizePath,
		"token_endpoint":                        p.baseURL + tokenPath,
		"registration_endpoint":                 p.baseURL + registerPath,
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post", "none"},
		"scopes_supported":                      []string{"mcp"},
	})
}

func (p *OAuthProvider) register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RedirectURIs            []string `json:"redirect_uris"`
		ClientName              string   `json:"client_name"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", err.Error())
		return
	}
	if len(body.RedirectURIs) == 0 || len(body.RedirectURIs) > 5 {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "one to five redirect_uris are required")
		return
	}
	for _, redirectURI := range body.RedirectURIs {
		if err := validateRedirectURI(redirectURI); err != nil {
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", err.Error())
			return
		}
	}
	authMethod := body.TokenEndpointAuthMethod
	if authMethod == "" {
		authMethod = "client_secret_post"
	}
	if authMethod != "client_secret_post" && authMethod != "none" {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "unsupported token_endpoint_auth_method")
		return
	}

	clientID, err := randomValue()
	if err != nil {
		http.Error(w, "failed to generate client", http.StatusInternalServerError)
		return
	}
	clientSecret := ""
	if authMethod == "client_secret_post" {
		clientSecret, err = randomValue()
		if err != nil {
			http.Error(w, "failed to generate client", http.StatusInternalServerError)
			return
		}
	}

	p.mu.Lock()
	p.cleanupLocked(time.Now())
	if len(p.clients) >= maxClients {
		p.mu.Unlock()
		oauthError(w, http.StatusTooManyRequests, "too_many_clients", "client limit reached")
		return
	}
	client := registeredClient{
		ID:              clientID,
		SecretHash:      hashValue(clientSecret),
		RedirectURIs:    slices.Clone(body.RedirectURIs),
		Name:            truncate(body.ClientName, 256),
		TokenAuthMethod: authMethod,
		CreatedAt:       time.Now(),
	}
	p.clients[clientID] = client
	p.mu.Unlock()
	if err := p.Save(); err != nil {
		http.Error(w, "failed to persist client", http.StatusInternalServerError)
		return
	}

	result := map[string]any{
		"client_id":                  clientID,
		"redirect_uris":              client.RedirectURIs,
		"client_name":                client.Name,
		"token_endpoint_auth_method": authMethod,
	}
	if clientSecret != "" {
		result["client_secret"] = clientSecret
	}
	writeJSON(w, http.StatusCreated, result)
}

func (p *OAuthProvider) authorize(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	clientID := query.Get("client_id")
	redirectURI := query.Get("redirect_uri")
	resource := query.Get("resource")
	if query.Get("response_type") != "code" {
		http.Error(w, "response_type must be code", http.StatusBadRequest)
		return
	}
	if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
		http.Error(w, "PKCE with S256 is required", http.StatusBadRequest)
		return
	}
	if resource != p.resourceURL {
		http.Error(w, "invalid resource", http.StatusBadRequest)
		return
	}
	if scope := query.Get("scope"); scope != "" && !strings.Contains(" "+scope+" ", " mcp ") {
		http.Error(w, "invalid scope", http.StatusBadRequest)
		return
	}

	code, err := randomValue()
	if err != nil {
		http.Error(w, "failed to start authorization", http.StatusInternalServerError)
		return
	}
	csrf, err := randomValue()
	if err != nil {
		http.Error(w, "failed to start authorization", http.StatusInternalServerError)
		return
	}
	p.mu.Lock()
	p.cleanupLocked(time.Now())
	client, ok := p.clients[clientID]
	if !ok || !slices.Contains(client.RedirectURIs, redirectURI) {
		p.mu.Unlock()
		http.Error(w, "unknown client or invalid redirect_uri", http.StatusBadRequest)
		return
	}
	if len(p.pending) >= maxPending {
		p.mu.Unlock()
		http.Error(w, "too many pending authorizations", http.StatusTooManyRequests)
		return
	}
	p.pending[code] = pendingAuthorization{
		ClientID:      clientID,
		RedirectURI:   redirectURI,
		CodeChallenge: query.Get("code_challenge"),
		State:         query.Get("state"),
		Resource:      resource,
		CSRF:          csrf,
		CreatedAt:     time.Now(),
	}
	p.mu.Unlock()

	renderApprovalPage(w, http.StatusOK, code, csrf, client.Name, "")
}

func (p *OAuthProvider) approve(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOAuthBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	code := r.Form.Get("code")
	csrf := r.Form.Get("csrf")
	password := r.Form.Get("password")

	p.mu.Lock()
	p.cleanupLocked(time.Now())
	pending, ok := p.pending[code]
	if !ok {
		p.mu.Unlock()
		http.Error(w, "invalid or expired authorization request", http.StatusBadRequest)
		return
	}
	if !constantEqual(csrf, pending.CSRF) {
		p.mu.Unlock()
		http.Error(w, "invalid request", http.StatusForbidden)
		return
	}
	if time.Now().Before(p.lockedUntil) {
		remaining := time.Until(p.lockedUntil)
		wait := ((remaining + time.Second - 1) / time.Second) * time.Second
		clientName := p.clients[pending.ClientID].Name
		p.mu.Unlock()
		renderApprovalPage(w, http.StatusTooManyRequests, code, pending.CSRF, clientName, fmt.Sprintf("Too many attempts. Retry in %s.", wait))
		return
	}
	if !constantEqual(password, p.password) {
		p.failed++
		if p.failed >= 5 {
			p.lockouts = min(p.lockouts+1, 10)
			p.lockedUntil = time.Now().Add(5 * time.Second * time.Duration(1<<(p.lockouts-1)))
		}
		newCSRF, err := randomValue()
		if err != nil {
			p.mu.Unlock()
			http.Error(w, "failed to continue authorization", http.StatusInternalServerError)
			return
		}
		pending.CSRF = newCSRF
		p.pending[code] = pending
		clientName := p.clients[pending.ClientID].Name
		lockedUntil := p.lockedUntil
		p.mu.Unlock()
		if !lockedUntil.IsZero() {
			remaining := time.Until(lockedUntil)
			wait := ((remaining + time.Second - 1) / time.Second) * time.Second
			renderApprovalPage(w, http.StatusTooManyRequests, code, newCSRF, clientName, fmt.Sprintf("Too many attempts. Retry in %s.", wait))
			return
		}
		renderApprovalPage(w, http.StatusUnauthorized, code, newCSRF, clientName, "Wrong password.")
		return
	}
	p.failed = 0
	p.lockouts = 0
	p.lockedUntil = time.Time{}
	pending.Approved = true
	p.pending[code] = pending
	p.mu.Unlock()

	redirect, _ := url.Parse(pending.RedirectURI)
	values := redirect.Query()
	values.Set("code", code)
	if pending.State != "" {
		values.Set("state", pending.State)
	}
	redirect.RawQuery = values.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (p *OAuthProvider) token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOAuthBody)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "invalid form")
		return
	}
	if r.Form.Get("resource") != p.resourceURL {
		oauthError(w, http.StatusBadRequest, "invalid_target", "invalid resource")
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		p.exchangeAuthorizationCode(w, r)
	case "refresh_token":
		p.exchangeRefreshToken(w, r)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "")
	}
}

func (p *OAuthProvider) exchangeAuthorizationCode(w http.ResponseWriter, r *http.Request) {
	code := r.Form.Get("code")
	clientID := r.Form.Get("client_id")
	verifier := r.Form.Get("code_verifier")

	p.mu.Lock()
	p.cleanupLocked(time.Now())
	pending, ok := p.pending[code]
	client, clientOK := p.clients[clientID]
	if !ok || !pending.Approved || !clientOK || pending.ClientID != clientID || pending.RedirectURI != r.Form.Get("redirect_uri") {
		p.mu.Unlock()
		oauthError(w, http.StatusBadRequest, "invalid_grant", "invalid authorization code")
		return
	}
	if !p.validClientSecretLocked(client, r.Form.Get("client_secret")) {
		p.mu.Unlock()
		oauthError(w, http.StatusUnauthorized, "invalid_client", "")
		return
	}
	challenge := sha256.Sum256([]byte(verifier))
	if base64.RawURLEncoding.EncodeToString(challenge[:]) != pending.CodeChallenge {
		p.mu.Unlock()
		oauthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}
	delete(p.pending, code)
	result, err := p.issueTokensLocked(clientID, pending.Resource, time.Now().Add(p.refreshTTL))
	p.mu.Unlock()
	if err != nil {
		http.Error(w, "failed to issue token", http.StatusInternalServerError)
		return
	}
	if err := p.Save(); err != nil {
		http.Error(w, "failed to persist token", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (p *OAuthProvider) exchangeRefreshToken(w http.ResponseWriter, r *http.Request) {
	refreshToken := r.Form.Get("refresh_token")
	clientID := r.Form.Get("client_id")
	refreshKey := hashValue(refreshToken)

	p.mu.Lock()
	p.cleanupLocked(time.Now())
	record, ok := p.refreshTokens[refreshKey]
	client, clientOK := p.clients[clientID]
	if !ok || !clientOK || record.ClientID != clientID || !p.validClientSecretLocked(client, r.Form.Get("client_secret")) {
		p.mu.Unlock()
		oauthError(w, http.StatusBadRequest, "invalid_grant", "invalid refresh token")
		return
	}
	delete(p.refreshTokens, refreshKey)
	delete(p.accessTokens, record.AccessTokenKey)
	result, err := p.issueTokensLocked(clientID, record.Resource, time.Now().Add(p.refreshTTL))
	p.mu.Unlock()
	if err != nil {
		http.Error(w, "failed to issue token", http.StatusInternalServerError)
		return
	}
	if err := p.Save(); err != nil {
		http.Error(w, "failed to persist token", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (p *OAuthProvider) issueTokensLocked(clientID, resource string, refreshExpiry time.Time) (map[string]any, error) {
	accessToken, err := randomValue()
	if err != nil {
		return nil, err
	}
	refreshToken, err := randomValue()
	if err != nil {
		return nil, err
	}
	accessKey := hashValue(accessToken)
	refreshKey := hashValue(refreshToken)
	p.accessTokens[accessKey] = tokenRecord{ClientID: clientID, Resource: resource, ExpiresAt: time.Now().Add(accessTokenTTL)}
	p.refreshTokens[refreshKey] = tokenRecord{ClientID: clientID, Resource: resource, ExpiresAt: refreshExpiry, AccessTokenKey: accessKey}
	return map[string]any{
		"access_token":  accessToken,
		"token_type":    "Bearer",
		"expires_in":    int(accessTokenTTL.Seconds()),
		"refresh_token": refreshToken,
		"scope":         "mcp",
	}, nil
}

func (p *OAuthProvider) validStaticToken(authorization string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) {
		return false
	}
	return constantEqual(strings.TrimPrefix(authorization, prefix), p.password)
}

func (p *OAuthProvider) validAccessToken(authorization string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) {
		return false
	}
	key := hashValue(strings.TrimPrefix(authorization, prefix))
	p.mu.Lock()
	defer p.mu.Unlock()
	record, ok := p.accessTokens[key]
	if !ok || time.Now().After(record.ExpiresAt) || record.Resource != p.resourceURL {
		delete(p.accessTokens, key)
		return false
	}
	return true
}

func (p *OAuthProvider) validClientSecretLocked(client registeredClient, secret string) bool {
	if client.TokenAuthMethod == "none" {
		return true
	}
	return constantEqual(client.SecretHash, hashValue(secret))
}

func (p *OAuthProvider) cleanupLocked(now time.Time) {
	for code, pending := range p.pending {
		if now.Sub(pending.CreatedAt) > pendingTTL {
			delete(p.pending, code)
		}
	}
	activeClients := make(map[string]bool)
	for key, record := range p.accessTokens {
		if now.After(record.ExpiresAt) {
			delete(p.accessTokens, key)
		} else {
			activeClients[record.ClientID] = true
		}
	}
	for key, record := range p.refreshTokens {
		if now.After(record.ExpiresAt) {
			delete(p.refreshTokens, key)
		} else {
			activeClients[record.ClientID] = true
		}
	}
	for id, client := range p.clients {
		if !activeClients[id] && now.Sub(client.CreatedAt) > clientTTL {
			delete(p.clients, id)
		}
	}
}

func (p *OAuthProvider) load() error {
	data, err := os.ReadFile(p.statePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read OAuth state: %w", err)
	}
	var state persistedOAuthState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode OAuth state: %w", err)
	}
	if state.Clients != nil {
		p.clients = state.Clients
	}
	if state.AccessTokens != nil {
		p.accessTokens = state.AccessTokens
	}
	if state.RefreshTokens != nil {
		p.refreshTokens = state.RefreshTokens
	}
	p.cleanupLocked(time.Now())
	return nil
}

func normalizeBaseURL(value string) (string, error) {
	value = strings.TrimRight(value, "/")
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("BASE_URL must be an absolute origin without a path")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !isLoopbackHost(u.Hostname())) {
		return "", fmt.Errorf("BASE_URL must use HTTPS unless it targets localhost")
	}
	return value, nil
}

func validateRedirectURI(value string) error {
	if len(value) > 2048 {
		return fmt.Errorf("redirect_uri is too long")
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" || u.Fragment != "" {
		return fmt.Errorf("redirect_uri must be an absolute URI without a fragment")
	}
	if u.Scheme == "https" || (u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return nil
	}
	return fmt.Errorf("redirect_uri must use HTTPS or target localhost")
}

func isLoopbackHost(host string) bool {
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}

func randomValue() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func hashValue(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func constantEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxOAuthBody)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func oauthError(w http.ResponseWriter, status int, code, description string) {
	payload := map[string]any{"error": code}
	if description != "" {
		payload["error_description"] = description
	}
	writeJSON(w, status, payload)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func renderApprovalPage(w http.ResponseWriter, status int, code, csrf, client, message string) {
	var page bytes.Buffer
	if err := approvalPage.Execute(&page, map[string]string{
		"Code": code, "CSRF": csrf, "Client": client, "Error": message,
	}); err != nil {
		http.Error(w, "failed to render authorization page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(page.Bytes())
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	result := make(map[K]V, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

var approvalPage = template.Must(template.New("approval").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Authorize org-syncd</title>
  <style>
    body { font-family: system-ui, sans-serif; max-width: 28rem; margin: 10vh auto; padding: 1.5rem; color: #202124; }
    h1 { font-size: 1.4rem; }
    label { display: block; margin: 1.5rem 0 .5rem; font-weight: 600; }
    input { box-sizing: border-box; width: 100%; padding: .7rem; font: inherit; }
    button { margin-top: 1rem; padding: .7rem 1rem; font: inherit; cursor: pointer; }
    .error { color: #b3261e; }
  </style>
</head>
<body>
  <h1>Authorize org-syncd</h1>
  <p>{{if .Client}}{{.Client}} is requesting{{else}}A client is requesting{{end}} access to your Org vault.</p>
  {{if .Error}}<p class="error">{{.Error}}</p>{{end}}
  <form method="post" action="/oauth/approve">
    <input type="hidden" name="code" value="{{.Code}}">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <label for="password">Server password</label>
    <input id="password" name="password" type="password" required autofocus autocomplete="current-password">
    <button type="submit">Authorize</button>
  </form>
</body>
</html>`))

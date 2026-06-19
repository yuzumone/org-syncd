package mcpserver

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	EndpointPath   = "/mcp"
	maxRequestBody = 4 << 20
)

var httpProtocolVersions = []string{"2025-06-18", "2025-03-26"}

// HTTPHandler exposes the stateless JSON-RPC subset of MCP Streamable HTTP.
func HTTPHandler(vault VaultBackend, authToken string) (http.Handler, error) {
	if authToken == "" {
		return nil, fmt.Errorf("HTTP MCP requires a non-empty auth token")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	mux.Handle(EndpointPath, bearerAuth(authToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			http.Error(w, "browser origins are not allowed", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if version := r.Header.Get("MCP-Protocol-Version"); version != "" && !contains(httpProtocolVersions, version) {
			http.Error(w, "unsupported MCP-Protocol-Version", http.StatusBadRequest)
			return
		}
		if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])); mediaType != "application/json" {
			http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
		if err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		var req request
		if err := json.Unmarshal(body, &req); err != nil {
			writeHTTPResponse(w, response{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &rpcError{Code: -32700, Message: "parse error"},
			})
			return
		}

		server := &rpcServer{vault: vault}
		resp := server.process(req)
		if resp == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeHTTPResponse(w, *resp)
	})))
	return mux, nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func bearerAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, "Bearer ") {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		provided := strings.TrimPrefix(authorization, "Bearer ")
		if len(provided) != len(token) || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeHTTPResponse(w http.ResponseWriter, resp response) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return
	}
}

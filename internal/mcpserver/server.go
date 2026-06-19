package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yuzumone/org-syncd/internal/orgvault"
)

type rpcServer struct {
	vault VaultBackend
}

type VaultBackend interface {
	ReadNote(path string) (orgvault.Note, error)
	WriteNote(path, content string) (orgvault.WriteResult, error)
	AppendNote(path, content string) (orgvault.WriteResult, error)
	ListFolders() ([]orgvault.Folder, error)
	ListNotes(opts orgvault.ListOptions) ([]orgvault.Note, error)
	SearchNotes(opts orgvault.SearchOptions) ([]orgvault.Match, error)
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *rpcServer) process(req request) *response {
	if len(req.ID) == 0 {
		return nil
	}
	resp := &response{JSONRPC: "2.0", ID: req.ID}
	result, rpcErr := s.handle(req)
	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		resp.Result = result
	}
	return resp
}

func (s *rpcServer) handle(req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		return map[string]any{
			"protocolVersion": selectProtocolVersion(params.ProtocolVersion),
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "org-vault-mcp",
				"version": "0.1.0",
			},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": tools()}, nil
	case "tools/call":
		var params toolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		result, err := s.callTool(params.Name, params.Arguments)
		if err != nil {
			return toolError(err), nil
		}
		return toolJSON(result)
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found"}
	}
}

func selectProtocolVersion(requested string) string {
	for _, version := range httpProtocolVersions {
		if requested == version {
			return version
		}
	}
	return httpProtocolVersions[0]
}

func (s *rpcServer) callTool(name string, args json.RawMessage) (any, error) {
	switch name {
	case "read_note":
		var in struct {
			Path string `json:"path"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		return s.vault.ReadNote(in.Path)
	case "write_note":
		var in struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		return s.vault.WriteNote(in.Path, in.Content)
	case "append_note":
		var in struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		return s.vault.AppendNote(in.Path, in.Content)
	case "list_folders":
		folders, err := s.vault.ListFolders()
		if err != nil {
			return nil, err
		}
		return struct {
			Folders []orgvault.Folder `json:"folders"`
		}{Folders: folders}, nil
	case "list_notes":
		var in struct {
			Folder        string `json:"folder"`
			Name          string `json:"name"`
			Tag           string `json:"tag"`
			ModifiedAfter string `json:"modified_after"`
			Sort          string `json:"sort"`
			Order         string `json:"order"`
			Limit         int    `json:"limit"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		opts := orgvault.ListOptions{
			Folder: in.Folder,
			Name:   in.Name,
			Tag:    in.Tag,
			Sort:   in.Sort,
			Order:  in.Order,
			Limit:  in.Limit,
		}
		if in.ModifiedAfter != "" {
			t, err := time.Parse("2006-01-02", in.ModifiedAfter)
			if err != nil {
				t, err = time.Parse(time.RFC3339, in.ModifiedAfter)
				if err != nil {
					return nil, fmt.Errorf("modified_after must be YYYY-MM-DD or RFC3339")
				}
			}
			opts.ModifiedAfter = t
		}
		notes, err := s.vault.ListNotes(opts)
		if err != nil {
			return nil, err
		}
		return struct {
			Notes []orgvault.Note `json:"notes"`
		}{Notes: notes}, nil
	case "search_notes":
		var in struct {
			Query  string `json:"query"`
			Folder string `json:"folder"`
			Limit  int    `json:"limit"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		matches, err := s.vault.SearchNotes(orgvault.SearchOptions{Query: in.Query, Folder: in.Folder, Limit: in.Limit})
		if err != nil {
			return nil, err
		}
		return struct {
			Matches []orgvault.Match `json:"matches"`
		}{Matches: matches}, nil
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func decodeArgs(args json.RawMessage, out any) error {
	if len(args) == 0 || bytes.Equal(args, []byte("null")) {
		args = []byte("{}")
	}
	return json.Unmarshal(args, out)
}

func toolJSON(result any) (any, *rpcError) {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]string{
			{"type": "text", "text": string(data)},
		},
	}, nil
}

func toolError(err error) map[string]any {
	return map[string]any{
		"isError": true,
		"content": []map[string]string{
			{"type": "text", "text": err.Error()},
		},
	}
}

func invalidParams(err error) *rpcError {
	return &rpcError{Code: -32602, Message: err.Error()}
}

func tools() []map[string]any {
	return []map[string]any{
		tool("read_note", "Read a note from the org vault.", map[string]any{
			"path": stringProp("Vault-relative note path."),
		}, []string{"path"}),
		tool("write_note", "Create or replace a note in CouchDB using the latest document revision.", map[string]any{
			"path":    stringProp("Vault-relative note path."),
			"content": stringProp("UTF-8 note content."),
		}, []string{"path", "content"}),
		tool("append_note", "Append UTF-8 content to a note in CouchDB. Retries once on revision conflict.", map[string]any{
			"path":    stringProp("Vault-relative note path."),
			"content": stringProp("UTF-8 content to append."),
		}, []string{"path", "content"}),
		tool("list_folders", "List folders in the org vault.", map[string]any{}, nil),
		tool("list_notes", "List .org notes in the org vault.", map[string]any{
			"folder":         stringProp("Optional folder path."),
			"name":           stringProp("Optional filename substring."),
			"tag":            stringProp("Optional org tag."),
			"modified_after": stringProp("Optional YYYY-MM-DD or RFC3339 timestamp."),
			"sort":           enumProp([]string{"name", "modified"}),
			"order":          enumProp([]string{"asc", "desc"}),
			"limit":          map[string]any{"type": "integer", "minimum": 1},
		}, nil),
		tool("search_notes", "Search text in .org notes.", map[string]any{
			"query":  stringProp("Search query."),
			"folder": stringProp("Optional folder path."),
			"limit":  map[string]any{"type": "integer", "minimum": 1},
		}, []string{"query"}),
	}
}

func tool(name, desc string, props map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return map[string]any{
		"name":        name,
		"description": desc,
		"inputSchema": schema,
	}
}

func stringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func enumProp(values []string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

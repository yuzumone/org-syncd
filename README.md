# org-syncd

`org-syncd` syncs a local directory of `.org`, `.md`, and `.txt` files with CouchDB.

Local files remain the primary editing interface. CouchDB stores one document per file with IDs like `file:tasks.org`.

## Configuration

All commands are configured with environment variables. The sync commands use:

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `LOCAL_DIR` | Yes | - | Local directory to synchronize |
| `COUCHDB_URL` | Yes | - | CouchDB server URL |
| `COUCHDB_DATABASE` | No | `orgsync` | CouchDB database |
| `COUCHDB_USER` | No | - | CouchDB username |
| `COUCHDB_PASSWORD` | No | - | CouchDB password |
| `DEVICE_ID` | No | hostname | Value stored in `updated_by` |
| `POLL_INTERVAL` | No | `5s` | Go duration between daemon sync cycles |
| `DRY_RUN` | No | `false` | Log planned writes without changing data |
| `INCLUDE_EXTS` | No | `.org,.md,.txt` | Comma-separated file extensions |
| `IGNORE` | No | `.git,.DS_Store,*.tmp` | Comma-separated ignore patterns |
| `LOG_LEVEL` | No | `info` | Structured log level |

## Commands

```bash
LOCAL_DIR=~/org COUCHDB_URL=http://localhost:5984 go run ./cmd scan
LOCAL_DIR=~/org COUCHDB_URL=http://localhost:5984 go run ./cmd download-only
LOCAL_DIR=~/org COUCHDB_URL=http://localhost:5984 go run ./cmd sync
LOCAL_DIR=~/org COUCHDB_URL=http://localhost:5984 go run ./cmd daemon
COUCHDB_URL=http://localhost:5984 go run ./cmd serve
```

Set `DRY_RUN=true` to log planned writes without changing CouchDB or local files.

```bash
LOCAL_DIR=~/org COUCHDB_URL=http://localhost:5984 DRY_RUN=true go run ./cmd sync
```

## Development

```bash
go test ./...
go build -o org-syncd ./cmd
```

## org-syncd HTTP Server

`org-syncd serve` runs a low-level MCP server over Streamable HTTP and a small
REST API for safely reading, writing, listing, searching, and appending
Org-mode files. It is intentionally workflow-neutral: GTD, inbox
processing, and refile workflows should be built by the AI client, skills, or
prompts using these primitives.

The HTTP server is CouchDB-first: it reads and writes CouchDB documents using
the existing org-syncd document model. Local file sync remains the job of
`org-syncd sync` or `org-syncd daemon`.

For an Ingress-facing HTTP server, set a long random bearer token. The MCP
endpoint is `POST /mcp`; `POST /api/files/append` appends UTF-8 content to a
CouchDB file path; `GET /healthz` is available for Kubernetes probes.

```bash
COUCHDB_URL=http://localhost:5984 \
COUCHDB_DATABASE=orgsync \
MCP_AUTH_TOKEN='replace-with-a-long-random-token' \
BASE_URL=https://org-vault.example.com \
DATA_DIR=./data \
HOST=0.0.0.0 \
PORT=8080 \
go run ./cmd serve
```

The server provides password-gated OAuth 2.1 with Dynamic Client Registration
and PKCE S256. A request without a valid `Authorization` header receives a 401
response pointing to OAuth protected-resource metadata. The client then opens
the authorization page, where the user enters `MCP_AUTH_TOKEN`. Static
`Authorization: Bearer <MCP_AUTH_TOKEN>` remains supported for curl and other
non-OAuth clients. The MCP endpoint path is fixed at `/mcp`.

HTTP server configuration is environment-only:

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `COUCHDB_URL` | Yes | - | CouchDB server URL |
| `COUCHDB_DATABASE` | No | `orgsync` | CouchDB database |
| `COUCHDB_USER` | No | - | CouchDB username |
| `COUCHDB_PASSWORD` | No | - | CouchDB password |
| `DEVICE_ID` | No | hostname | Value stored in `updated_by` |
| `HOST` | No | `0.0.0.0` | HTTP bind address |
| `PORT` | No | `8080` | HTTP port |
| `MCP_AUTH_TOKEN` | Yes | - | OAuth approval password and static bearer token |
| `BASE_URL` | No | `http://localhost:PORT` | Public OAuth issuer URL; use HTTPS outside localhost |
| `DATA_DIR` | No | `~/.org-syncd` | Persistent OAuth state directory |
| `MCP_REFRESH_DAYS` | No | `14` | Refresh token inactivity lifetime in days |

For writes, `updated_by` is selected in this order:

1. `DEVICE_ID`
2. hostname
3. `mcp`

Available MCP tools:

- `read_note`: read one note by CouchDB file path.
- `write_note`: create or replace a note. It fetches the latest `_rev` and
  overwrites that document.
- `append_note`: append content to a note. It retries once on `_rev` conflict by
  re-reading and appending to the latest content.
- `list_folders`: discover real folders and immediate `.org` note counts.
- `list_notes`: list `.org` notes with optional folder, name, tag, modified date,
  sort, order, and limit filters.
- `search_notes`: case-insensitive full-text search over `.org` notes.

Available REST APIs:

- `POST /api/files/append`: append UTF-8 content to a CouchDB file path.

Example REST append request:

```bash
curl https://org-vault.example.com/api/files/append \
  -H 'Authorization: Bearer replace-with-a-long-random-token' \
  -H 'Content-Type: application/json' \
  --data '{"path":"inbox.org","content":"\n* TODO sample\n"}'
```

Safety behavior:

- Paths are relative CouchDB file document paths.
- `../`, hidden paths, and `.backup` paths are rejected or skipped.
- Writes use documents shaped like `file:{path}` with `type=file`, `path`,
  `content`, `content_sha256`, `mtime`, `deleted=false`, and `updated_by`.
- Note content must be UTF-8.

Example HTTP initialize request:

```bash
curl https://org-vault.example.com/mcp \
  -H 'Authorization: Bearer replace-with-a-long-random-token' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"1"}}}'
```

Codex can register and authenticate through OAuth without placing the static
bearer token in its environment:

```bash
codex mcp add org_vault \
  --url https://org-vault.example.com/mcp \
  --oauth-resource https://org-vault.example.com/mcp
codex mcp login org_vault --scopes mcp
```

For direct bearer authentication instead, configure
`bearer_token_env_var = "ORG_VAULT_MCP_TOKEN"` in `~/.codex/config.toml`.

## Container

Build the image, publish port 8080, and provide `MCP_AUTH_TOKEN`:

```bash
docker run --rm -p 8080:8080 \
  -v org-syncd-data:/data \
  -e COUCHDB_URL=http://couchdb:5984 \
  -e COUCHDB_DATABASE=orgsync \
  -e MCP_AUTH_TOKEN='replace-with-a-long-random-token' \
  -e BASE_URL=https://org-vault.example.com \
  -e DATA_DIR=/data/org-syncd \
  -e HOST=0.0.0.0 \
  -e PORT=8080 \
  org-syncd serve
```

The Kubernetes example at `deploy/k8s/org-vault-mcp.yaml` expects an
`org-vault-mcp-auth` Secret with a `token` key. OAuth clients and tokens are
stored on the `org-vault-mcp-oauth` PVC. Keep this deployment at one replica.
Replace the example public URL, Ingress host, TLS secret, storage class, and
ingress class before applying it.

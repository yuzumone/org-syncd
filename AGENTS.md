# AGENTS.md

## Project

`org-syncd` is a Go daemon that syncs a local directory of text files, mainly org-mode files, with CouchDB.

Goal:

```
local directory
  ↕
org-syncd
  ↕
CouchDB
```

CouchDB is the shared sync database. Local files remain the primary editing interface for Emacs/org-mode.

The repository also contains a low-level Org vault MCP server:

```
AI client
  ↕ MCP Streamable HTTP
org-syncd mcp
  ↕
Org-mode / Org-roam vault
```

The MCP server is for safe note primitives only. Keep GTD, inbox cleanup,
refile, project extraction, and other workflows outside the MCP tool layer;
compose those in AI prompts, skills, or clients.

MCP is CouchDB-only: `org-syncd mcp` reads and writes CouchDB directly, while
local file sync remains a separate client/daemon concern.

## MVP Scope

Implement a minimal, reliable sync daemon.

### Required features

- Watch a configured local directory.
- Sync `.org`, `.md`, `.txt` files.
- Store each file as one CouchDB document.
- Push local file changes to CouchDB.
- Pull remote CouchDB changes to local files.
- Detect basic conflicts.
- Avoid infinite sync loops.
- Support dry-run mode and download only.
- Provide clear logs.

### Out of scope for MVP

- Binary file sync.
- PDF/image attachment sync.
- Full CRDT merge.
- Org AST parsing.

## Org Vault MCP Server

Run with:

```bash
COUCHDB_URL=http://localhost:5984 COUCHDB_DATABASE=orgsync MCP_AUTH_TOKEN=secret org-syncd mcp
```

All MCP configuration is provided through environment variables. Do not add MCP
configuration flags. `updated_by` selection order:

1. `DEVICE_ID`
2. hostname
3. `mcp`

### MCP tool scope

Keep MCP tools workflow-neutral and close to file/vault operations.

Implemented tools:

- `read_note`: read one vault-relative note.
- `write_note`: create or replace one note.
- `append_note`: append content to one note.
- `list_folders`: discover folders and note counts.
- `list_notes`: list `.org` notes with optional filters.
- `search_notes`: full-text search `.org` notes.

Do not add workflow-specific tools such as:

- `refile_to_project`
- `extract_inbox_item`
- `create_next_action`

Future Org subtree operations are acceptable if they stay generic, for example
read subtree, replace subtree, append subtree, or list headings.

### MCP safety requirements

- Never write CouchDB documents outside the existing org-syncd file document
  model.
- Reject `../` path traversal.
- Reject or skip hidden paths by default.
- Exclude `.backup` from list and search results.
- Require UTF-8 content.
- `write_note` fetches the latest `_rev` and overwrites that document.
- `append_note` retries once on `_rev` conflict by re-reading the latest
  document and appending again.
- Keep the implementation usable over Streamable HTTP JSON-RPC MCP transport.
- Keep the MCP endpoint fixed at `/mcp`.
- Support password-gated OAuth 2.1 with DCR and PKCE S256. Keep direct static
  bearer authentication for non-OAuth clients.
- Advertise OAuth discovery through `WWW-Authenticate` on unauthorized MCP
  requests.
- Persist hashed OAuth client and token state under `DATA_DIR` using atomic
  writes and owner-only file permissions. Kubernetes deployments use one
  replica and a PVC for this state.
- Reject requests to `/mcp` with a browser `Origin` header by default.

## Document Model

Each file maps to one CouchDB document.

Example:

```json
{
    "_id": "file:tasks.org",
    "_rev": "...",
    "type": "file",
    "path": "tasks.org",
    "content": "* TODO sample\n",
    "content_sha256": "...",
    "mtime": "2026-06-06T15:00:00+09:00",
    "deleted": false,
    "updated_by": "macbook"
}
```

Rules:

- `_id` is `file:` + relative path.
- `path` must be relative to `local_dir`.
- Never allow `../` path traversal.
- `content_sha256` is SHA-256 of file content bytes.
- `updated_by` is the configured `device_id`.
- Deleted files should be represented with `deleted: true`, not immediately purged.

## Config

Use YAML config.

Example:

```yaml
device_id: macbook
local_dir: /Users/yuzumone/org
couchdb_url: http://localhost:5984
database: orgsync
username: admin
password: password
poll_interval: 5s
dry_run: false
include_exts:
  - .org
  - .md
  - .txt
ignore:
  - .git
  - .DS_Store
  - "*.tmp"
```

## Sync Behavior

### Startup

1. Load config.
2. Ensure CouchDB database exists.
3. Scan local directory.
4. Fetch remote docs.
5. Compare by `content_sha256`.
6. Push local-only files.
7. Pull remote-only files.
8. If both changed, create conflict file.

### Local change

When a watched file changes:

1. Read file.
2. Compute SHA-256.
3. Skip if hash equals last pulled/pushed hash.
4. Fetch remote doc.
5. PUT updated document with latest `_rev`.

### Remote change

Poll CouchDB `_changes`.

1. Ignore docs where `updated_by == device_id`.
2. Pull changed docs.
3. Write local file.
4. Update local state to avoid push loop.

## Conflict Handling

MVP conflict behavior:

    tasks.org
    tasks.conflict-20260606-153000.org

Rules:

- Never silently overwrite local changes.
- If local hash differs from last known hash and remote also changed, preserve both.
- Keep local file as-is.
- Write remote version to `*.conflict-YYYYMMDD-HHMMSS.ext`.
- Log conflict clearly.

## CouchDB API

Use standard `net/http`.

Required endpoints:

    PUT /{db}
    GET /{db}/{docid}
    PUT /{db}/{docid}
    GET /{db}/_all_docs?include_docs=true
    GET /{db}/_changes?since={seq}&include_docs=true

Use Basic Auth if username/password are set.

## Safety Requirements

- Do not sync files outside `local_dir`.
- Do not follow symlinks by default.
- Do not delete local files permanently in MVP.
- Do not sync hidden files unless explicitly included.
- Use atomic writes:
  - write to temp file
  - fsync if practical
  - rename into place
- Debounce file watcher events.

## Logging

Use structured logs.

Log levels:

- info: startup, scan summary, push, pull
- warn: conflict, skipped file, retry
- error: CouchDB failure, file write failure

Example:

```
INFO pushed path=tasks.org sha=...
INFO pulled path=inbox.org rev=...
WARN conflict path=projects.org conflict=projects.conflict-20260606-153000.org
```

## Tests

Add unit tests for:

- path normalization
- doc ID generation
- SHA-256 calculation
- conflict filename generation
- config loading
- ignored file matching
- Org vault path safety
- Org vault CouchDB write behavior
- Org vault append conflict retry
- Org vault listing and search over CouchDB documents

Prefer table-driven Go tests.

## Implementation Order

1. Config loader.
2. File scanner.
3. Hash utilities.
4. CouchDB client.
5. One-shot download only command.
6. One-shot sync command.
7. File watcher.
8. `_changes` polling.
9. Conflict handling.
10. Dry-run mode.
11. README with usage examples.

## Commands

Target commands:

```bash
go run ./cmd --config config.yaml
COUCHDB_URL=http://localhost:5984 go run ./cmd mcp
go test ./...
go build -o org-syncd ./cmd
```

Optional later:

```bash
org-syncd scan
org-syncd download-only
org-syncd sync --once
org-syncd daemon
org-syncd mcp
```

## Code Style

- Keep functions small.
- Prefer explicit error handling.
- Avoid global mutable state.
- Use contexts for HTTP requests.
- Add timeouts to HTTP clients.
- Do not introduce large frameworks.
- Keep dependencies minimal.

## Future Ideas

After MVP:

- Binary file support.
- Per-file encryption.
- Prometheus metrics.
- Health check endpoint.

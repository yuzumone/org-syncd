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
go test ./...
go build -o org-syncd ./cmd
```

Optional later:

```bash
org-syncd scan
org-syncd download-only
org-syncd sync --once
org-syncd daemon
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

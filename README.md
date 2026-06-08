# org-syncd

`org-syncd` syncs a local directory of `.org`, `.md`, and `.txt` files with CouchDB.

Local files remain the primary editing interface. CouchDB stores one document per file with IDs like `file:tasks.org`.

## Configuration

```yaml
device_id: macbook
local_dir: /Users/yuzumone/org
couchdb_url: http://localhost:5984
database: orgsync
username: admin
password: password
poll_interval: 5s
include_exts:
  - .org
  - .md
  - .txt
ignore:
  - .git
  - .DS_Store
  - "*.tmp"
```

## Commands

```bash
go run ./cmd --config config.yaml scan
go run ./cmd --config config.yaml download-only
go run ./cmd --config config.yaml sync
go run ./cmd --config config.yaml daemon
```

Use `--dry-run` to log planned writes without changing CouchDB or local files.

```bash
go run ./cmd --config config.yaml --dry-run sync
```

## Development

```bash
go test ./...
go build -o org-syncd ./cmd
```

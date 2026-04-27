---
rfd: 0003
title: V1 Server Implementation
status: committed
created: 2026-04-26
authors:
  - gdey
tags: [design, server, go, http]
related: [0001, 0004, 0005]
superseded-by: ~
---

# V1 Server Implementation

## Summary

The implementation plan for the local HTTP server described in RFD 0001. This RFD turns the architecture into something concrete enough to build: project layout, endpoint shapes, config and history file formats, CLI surface, and dependency choices.

## Scope

In scope: the v1 server binary — endpoints, auth, CORS, vault writing, config and history storage, CLI subcommands for setup and vault management, logging, errors, testing strategy, build/distribute.

Out of scope: the Firefox extension (separate RFD), the click-element capture flow (RFD 0002), per-vault configurable layout, the local agent integration (future), token rotation flows, packaged installers (`brew`, `.msi`, etc.).

## Project layout

Standard Go project structure. Single binary, multiple subcommands.

```
outcrop/
├── cmd/
│   └── outcrop/
│       └── main.go            # CLI entrypoint, subcommand dispatch
├── server/                    # HTTP server + handlers + middleware
├── store/                     # SQLite access, schema, migrations
│   └── migrations/            # goose .sql files, embedded
├── vault/                     # vault path resolution + atomic file writes
├── clip/                      # clip-writing orchestration
├── cli/                       # subcommand implementations
├── docs/
│   └── rfd/                   # this directory
└── go.mod
```

Packages live at the root rather than under `internal/`. We are not publishing this as a library; the import-prevention `internal/` provides isn't earning its keep here.

## HTTP server

### Framework: `net/http` from the standard library

Go 1.22+ has good enough method+path routing in `http.ServeMux` (`mux.HandleFunc("GET /vaults", ...)`) for our purposes. No external HTTP framework. Reasons:

- Three endpoints. Routing complexity does not warrant a dependency.
- One fewer thing to keep updated, audit, or replace.
- Middleware (auth, CORS, logging, recovery) is short enough to write directly.

If routing complexity grows past stdlib's comfort zone we can revisit, but YAGNI for v1.

### Endpoints

All routes require auth. All routes return JSON (or empty body on success where noted).

#### `GET /healthz`

Liveness check for the extension to confirm the server is reachable and the token is valid.

- Response: `200 OK` `{"status": "ok"}`

#### `GET /vaults`

Returns the list of configured vaults, ranked by relevance when context is provided.

- Query params (optional):
  - `url` — the page URL the user is about to clip
  - `title` — the page title
- Response: `200 OK`
  ```json
  [
    {"key": "01HZX9F8K7B2YQE6M4...", "displayName": "Work Notes", "isDefault": false},
    {"key": "01HZX9F8K7B2YQE6M5...", "displayName": "Personal",   "isDefault": true}
  ]
  ```
- Without `url`/`title`: alphabetical by `displayName`, with `isDefault` flagged.
- With `url`/`title`: ranked by the v1 heuristic (most-recently-used vault for the same registrable domain, falling back to default, then alphabetical).

#### `POST /clip`

Writes a clip to the named vault.

- Request body:
  ```json
  {
    "vault": "01HZX9F8K7B2YQE6M4...",
    "url": "https://example.com/article",
    "title": "Page Title",
    "selectedText": "...",
    "notes": "...",
    "imageBase64": "..."
  }
  ```
- Response: `200 OK`
  ```json
  {
    "notePath":  "Clippings/clip-20260426T143200-471.md",
    "imagePath": "Clippings/attachments/clip-20260426T143200-471.png"
  }
  ```
  Paths are vault-relative — the server still does not leak absolute on-disk locations.
- Errors:
  - `400` — malformed body, unknown vault key, image decode failure
  - `401` — missing or wrong token
  - `500` — disk write failed

### Auth

Single shared-secret token, sent as `Authorization: Bearer <token>` on **every** request including `/healthz`.

- Token is generated at `outcrop init` (32 bytes from `crypto/rand`, base64url-encoded).
- Stored in the config file alongside listen address and vault list.
- Compared with `crypto/subtle.ConstantTimeCompare` to avoid timing leaks.
- Rotation: delete from config and re-run `outcrop init --force` (token rotation flows are explicitly out of scope for v1).

### CORS

The Firefox extension calls from a `moz-extension://<uuid>` origin. The UUID is per-install and not knowable in advance.

Policy:

- On preflight (`OPTIONS`): if the `Origin` header begins with `moz-extension://`, echo it back in `Access-Control-Allow-Origin`. Otherwise reject.
- Allow methods: `GET, POST, OPTIONS`.
- Allow headers: `Authorization, Content-Type`.
- `Access-Control-Max-Age: 600` to reduce preflight chatter.

The server is bound to loopback and authenticated; the CORS check is belt-and-braces, not the primary defence.

### Listen address

Default `127.0.0.1:7878`. Configurable in the config file. Never bind to anything other than loopback addresses (`127.0.0.0/8` or `::1`) — startup must reject non-loopback bindings explicitly.

### Middleware order

`recover → log → cors → auth → handler`

- `recover` — catch panics, return `500`, log the stack
- `log` — structured request log (method, path, status, duration)
- `cors` — handle `OPTIONS`, set `Access-Control-Allow-*` headers
- `auth` — verify Bearer token (skipped for `OPTIONS`)
- `handler` — the actual route

## Storage

Both configuration and history live in a single SQLite database.

### Location

Use `os.UserConfigDir()`:

- Linux: `$XDG_CONFIG_HOME/outcrop/outcrop.db` (default `~/.config/outcrop/outcrop.db`)
- macOS: `~/Library/Application Support/outcrop/outcrop.db`
- Windows: `%AppData%\outcrop\outcrop.db`

### Driver: `modernc.org/sqlite`

Pure-Go SQLite (transpiled from the C source). Reasons:

- No cgo. Cross-compilation (`GOOS=windows go build`) just works.
- Keeps the "one static binary per OS" story intact.
- Perfectly adequate performance for our workload (a few vaults, one write per clip).

`mattn/go-sqlite3` is faster but cgo's cross-compile tax isn't worth it here.

### PRAGMAs

Run on every connection open:

```sql
PRAGMA journal_mode = WAL;       -- crash-resilient, better concurrent read/write
PRAGMA synchronous = NORMAL;     -- WAL is safe at NORMAL; FULL is overkill
PRAGMA foreign_keys = ON;        -- required for FK cascades to work
PRAGMA busy_timeout = 5000;      -- 5s, in case CLI and server both touch the DB
```

### Schema (v1)

The v1 schema is defined in `store/migrations/00001_init.sql`. Conceptually:

```sql
-- Key/value for singleton config items
CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
-- meta keys: token, listen_addr, default_vault_key

CREATE TABLE vaults (
  key             TEXT PRIMARY KEY,        -- ULID
  display_name    TEXT NOT NULL,
  path            TEXT NOT NULL,           -- absolute, server-only
  clipping_path   TEXT NOT NULL DEFAULT 'Clippings',
  attachment_path TEXT NOT NULL DEFAULT 'Clippings/attachments',
  created_at      INTEGER NOT NULL         -- unix seconds, UTC
);
CREATE INDEX vaults_display_name ON vaults(display_name);

CREATE TABLE history (
  domain     TEXT NOT NULL,             -- registrable domain (eTLD+1)
  vault_key  TEXT NOT NULL,
  count      INTEGER NOT NULL DEFAULT 0,
  last_used  INTEGER NOT NULL,          -- unix seconds, UTC
  PRIMARY KEY (domain, vault_key),
  FOREIGN KEY (vault_key) REFERENCES vaults(key) ON DELETE CASCADE
);
CREATE INDEX history_domain_lastused ON history(domain, last_used DESC);
```

The `path` column is the only place absolute filesystem paths exist in the running system. It never leaves the server.

`ON DELETE CASCADE` on `history.vault_key` means removing a vault automatically prunes its history — no manual cleanup logic in the vault-remove CLI.

### Migrations: `github.com/gdey/goose/v3`

Migrations are managed by goose. They live as numbered `.sql` files under `store/migrations/`, embedded into the binary with `//go:embed`:

```
store/migrations/
├── 00001_init.sql
└── ...
```

On every startup (server or CLI), the DB is opened and goose's `Up` is run against the embedded migrations. New migrations get picked up automatically; an already-up-to-date DB is a no-op.

Goose tracks state in its own `goose_db_version` table. We don't use `PRAGMA user_version` for this — leaving goose as the single source of truth.

If the binary is older than the DB (i.e., the DB has a migration version this binary doesn't know about), goose surfaces this; the startup path treats it as fatal and exits with a clear message ("DB schema is newer than this binary; upgrade outcrop").

Down-migrations are written for completeness but never run automatically. Manual recovery only.

### Loading and concurrency

- The server holds an open connection (or small pool) for its lifetime.
- CLI subcommands open the DB, do their thing, close. Both can run concurrently — WAL mode + `busy_timeout` handle the contention.
- No hot-reload in v1. CLI changes take effect on the next request the server handles (since each handler reads what it needs from the DB) — but listen address changes still need a server restart.

### Atomic writes

SQLite handles this. No more temp-file-and-rename helpers; transactions give us atomicity for free.

## History

### Recording

On every successful `POST /clip`, the server upserts a single row:

```sql
INSERT INTO history (domain, vault_key, count, last_used)
VALUES (?, ?, 1, ?)
ON CONFLICT(domain, vault_key) DO UPDATE SET
  count = count + 1,
  last_used = excluded.last_used;
```

Domain is computed via `golang.org/x/net/publicsuffix` so `news.example.com` and `blog.example.com` share history under `example.com`.

### Ranking (v1 heuristic)

For a given URL:

1. Compute registrable domain.
2. Query history for that domain ordered by `last_used DESC`. The returned `vault_key`s are the ranked head of the list.
3. Append any vault keys not represented in history, alphabetical by display name.
4. The configured default is always included (it's a row in `vaults`); the `isDefault` flag in the API response is set for whichever entry matches `default_vault_key` regardless of its position.

If `url` is absent: alphabetical by display name, default flagged.

### Pruning

Removing a vault via `outcrop vault remove <key>` deletes the row from `vaults`; FK cascade drops the history rows. No application-level pruning logic needed.

## Clip writing

### Filenames

UTC timestamp with millisecond resolution to make collisions effectively impossible:

```
clip-20260426T143200-471.md
clip-20260426T143200-471.png
```

The note and image share the timestamp. If by some accident two clips collide on the same millisecond, the second write fails (the server uses `O_EXCL` open) and the handler retries with the next millisecond.

### Vault layout (per RFD 0001)

The note and image paths within a vault come from the vault's `clipping_path` and `attachment_path` columns:

```
<vault.path>/<vault.clipping_path>/<filename>.md
<vault.path>/<vault.attachment_path>/<filename>.png
```

For v1, both columns always have their defaults (`Clippings` and `Clippings/attachments`) — there's no CLI to change them. v1.1 will add a subcommand to override.

Both paths are stored without trailing slashes (canonical form). Both must be vault-relative — no leading `/`, no `..` segments. This is validated when a vault is added or updated, and rechecked at write time so a tampered DB cannot be used to escape the vault root.

Server creates the resolved directories on first use if they don't exist.

### Note body

```markdown
# Clipped from [<title>](<url>)

*Captured <UTC timestamp, formatted: 2006-01-02 15:04 UTC>*

![[<image-filename>.png]]

## Notes

<notes-from-extension>

## Selected Text

<selectedText, if any, fenced in a blockquote>
```

Sections with no content are omitted (no `## Notes` heading if `notes` is empty).

### Write order

1. Decode the base64 image into a `[]byte`.
2. Open `<vault>/Clippings/attachments/<name>.png` with `O_WRONLY|O_CREATE|O_EXCL`. Write, fsync, close.
3. Write the markdown note via the temp-file-and-rename pattern.
4. Record history.
5. Return paths.

If step 2 or 3 fails, clean up partial files. If step 4 fails, the clip is still written — log the error but return success to the extension. (History is best-effort; a clip on disk is the source of truth.)

### Filename hygiene

Title and notes are never used in filenames. The H1 in the note body uses the title verbatim (no sanitization needed for markdown content).

## CLI subcommands

Single binary, subcommands using `flag` package or stdlib `flag.NewFlagSet` per subcommand. No CLI framework dependency for v1.

```
outcrop init [--force]            # generate token, write initial config
outcrop serve                     # start the HTTP server
outcrop vault add <name> <path>   # add a vault, prints the assigned key
outcrop vault list                # list vaults (key, name, path, default)
outcrop vault rename <key> <name> # change display name
outcrop vault remove <key>        # remove (and prune history)
outcrop vault default <key>       # set default
outcrop config show               # print current config (with token redacted unless --show-token)
outcrop config path               # print config file path
```

`outcrop init` flow:

1. Refuse if config exists, unless `--force`.
2. Create config dir if missing.
3. Generate token, write skeleton config (no vaults yet).
4. Print the token to stdout with copy instructions.
5. Tell the user to add a vault next.

`outcrop vault add` flow:

1. Require `<path>` to exist and be a directory. Refuse otherwise — outcrop is not in the business of provisioning vault directories. The user creates the Obsidian vault first (or points at an existing one), then registers it.
2. Resolve to an absolute, symlink-cleaned path before storing.
3. Generate a ULID, insert the row with default `clipping_path` and `attachment_path`.
4. Print the assigned key.

## Logging

`log/slog` (stdlib, Go 1.21+). Text handler by default; `--log-format=json` flag for structured logs if needed.

Levels:

- `INFO` — request completed, vault added/removed, server started/stopped.
- `WARN` — recoverable errors (history write failed, malformed request).
- `ERROR` — server-side failures (disk write failed, panic recovered).

Never log the token. Never log `imageBase64` content. Never log `selectedText` or `notes` body — they could contain anything the user clipped (passwords from a page, etc.). **URLs and titles are also not logged**: they can carry sensitive content (session tokens in query strings, private page titles), and request logs that say "POST /clip 200 4ms" are sufficient. If a debugging need arises later, we can add an explicit `--debug-requests` flag; not building it speculatively.

## Errors

JSON shape:

```json
{"error": "vault_not_found", "message": "no vault with key 01HZX..."}
```

`error` is a stable machine-readable code. `message` is human-readable and may change.

Codes for v1: `bad_request`, `unauthorized`, `vault_not_found`, `image_decode_failed`, `disk_write_failed`, `internal`.

## Testing

- HTTP handlers: `httptest.NewRecorder` + `httptest.NewServer` for end-to-end.
- Vault writes: temp dirs via `t.TempDir()`, assert files exist with the right content.
- History: round-trip serialization tests, ranking tests with hand-built history.
- Atomic write helper: its own unit test (write, kill mid-write, assert no partial file).
- No mocking frameworks. Plain interfaces and table tests.

## Build and distribute

For v1: `go build ./cmd/outcrop` produces a single static binary. Cross-compile via `GOOS`/`GOARCH`. No installer story for v1; the user runs the binary by hand and follows instructions for launch-on-login (see RFD 0001).

`goreleaser` and packaged installers are post-v1.

## Dependencies

Direct dependencies kept minimal:

- `github.com/oklog/ulid/v2` — ULID generation.
- `golang.org/x/net/publicsuffix` — registrable domain extraction.
- `modernc.org/sqlite` — pure-Go SQLite driver (no cgo).
- `github.com/gdey/goose/v3` — schema migrations.

No HTTP framework, no logging library, no config library, no CLI library.

## Deferred concerns

Not open questions for v1 — decisions made or consciously deferred:

- **History growth.** Append-forever per `(domain, vault)` is bounded only by the number of distinct domains the user has clipped from. For a single human user this stays small for years. No compaction story in v1. Add one if it ever becomes a problem.

## Build order (within v1)

Refining RFD 0001's build order with the structure above:

1. ✅ DB open/migrate + `outcrop init` (creates DB, generates token, writes initial `meta`).
2. ✅ `outcrop vault add/list/remove/rename/default` — `vaults` table CRUD.
3. ✅ `outcrop serve` skeleton: listen, log, healthz, auth middleware, CORS.
4. ✅ `GET /vaults` (alphabetical, no ranking yet).
5. ✅ `POST /clip` writing into a vault. End-to-end with `curl`.
6. ✅ History recording + ranking applied to `GET /vaults`.
7. ✅ Polish: error shapes, logging, tests filling in gaps.

By step 5 the server is usable end-to-end against a hand-built request and can be wired to the extension once that's built.

## Status notes

- 2026-04-26 — Created as `discussion`.
- 2026-04-27 — Implementation landed in commit `6600b45`. All seven build-order steps complete. Manual end-to-end validated by hand (`outcrop init` → `outcrop vault add` → `outcrop serve` → `curl POST /clip`). Test gaps closed: `cli/`, `server/clip.go`, and `server/health.go` now have direct unit tests; `go test ./...` is clean across all packages. Promoted to `committed`. Future server changes happen as new RFDs (or amendments to this one with explicit deltas), not as in-place rewrites — this document is now reference, not proposal.
- 2026-04-27 — Schema amended by RFD 0005 (still `draft`): migration `00002_vault_description.sql` adds an optional `vault.description` column for the LLM prompt formatter. Backfills as empty string; pre-existing flows unaffected. The column is server-internal (not exposed via `GET /vaults`). See RFD 0005 §"Schema and vault descriptions" for the migration and CLI surface.
- 2026-04-27 — Wire shape of `GET /vaults` amended by RFD 0005: the response was an array; it becomes `{vaults: [...], agent: {enabled: bool}}` so the popup can learn whether the agent is enabled in a single trip. Breaking change for consumers, but the Firefox extension is the only one and ships in lock-step. The richer agent status — capabilities, version, local-vs-remote — is reserved for the future `GET /agent/status` endpoint scoped to RFD 0006 (`ideation`).

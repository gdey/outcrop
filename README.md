# Outcrop

<img src="logo.svg" alt="logo" width="50%"/> <br>

Clip a region of a webpage into an Obsidian vault. Drag a rectangle in Firefox; a markdown note — with the source URL, your annotations, and the cropped PNG — lands in an Obsidian vault on disk.

Two halves, both running on the user's machine. No cloud, no account.

- **Firefox extension** (`extension/firefox/`) — selection overlay, screen capture, preview-with-notes UI.
- **Local Go server** (`cmd/outcrop/`) — receives the clip over loopback, writes the note and image into the right vault, tracks per-domain history so future captures default to the most-recently-used vault for the page.

```
┌─────────────────────┐   GET /vaults?url=&title=   ┌──────────────────────┐
│ Firefox extension   │ ──────────────────────────▶ │ outcrop serve        │ ─▶ Obsidian vault(s)
│ - drag overlay      │   POST /clip                │ - 127.0.0.1:7878     │   (filesystem)
│ - preview + notes   │ ──────────────────────────▶ │ - SQLite-backed      │
└─────────────────────┘                             └──────────────────────┘
```

The full design is in [`docs/rfd/`](docs/rfd/). The README in that directory explains the RFD format and lifecycle.

## Status

| Component | Status | RFD |
|---|---|---|
| Architecture overview | `discussion` | [0001](docs/rfd/0001-architecture-overview.md) |
| V1 server | `committed` | [0003](docs/rfd/0003-v1-server.md) |
| Firefox extension | `draft` | [0004](docs/rfd/0004-firefox-extension.md) |
| Local-LLM vault routing (in-process kronk) | `committed` | [0005](docs/rfd/0005-local-llm-vault-recommendation.md) |
| Training-data capture | `accepted` (capture-only; export deferred) | [0011](docs/rfd/0011-training-data-capture.md) |
| Click-element capture (post-v1) | `ideation` | [0002](docs/rfd/0002-click-element-capture.md) |

## Server

Prerequisites: Go (see `go.mod` for the directive).

```sh
go build ./cmd/outcrop
./outcrop init                                # writes config DB, prints a token

# Register an Obsidian vault. A description is strongly recommended if you
# plan to use the LLM router (RFD 0005) — vaults without one are at a
# routing disadvantage versus vaults that have one.
./outcrop vault add \
    --description "life admin, journaling, news, things to remember" \
    Personal /path/to/Vault

./outcrop serve                               # listens on 127.0.0.1:7878
```

Save the token printed by `init` — you'll paste it into the extension on first run.

The wire contract is small enough to drive by hand:

```sh
TOKEN=...   # from `outcrop init`
VAULT=...   # from `outcrop vault list`
IMG=$(base64 -i some.png)

curl -sS -H "Authorization: Bearer $TOKEN" \
        -H "Content-Type: application/json" \
        -d "{\"vault\":\"$VAULT\",\"url\":\"https://example.com\",\"title\":\"Example\",\"selectedText\":\"\",\"notes\":\"\",\"imageBase64\":\"$IMG\"}" \
        http://127.0.0.1:7878/clip
```

Other CLI subcommands:

```
outcrop vault list
outcrop vault show <key>
outcrop vault rename <key> <newName>
outcrop vault describe <key> "<description>"
outcrop vault remove <key>
outcrop vault default <key>
outcrop config show [--show-token]
outcrop config path
```

Tests: `go test ./...`.

## Local LLM (optional)

Outcrop can rank vaults with a local LLM so the popup pill is the right vault more often. The agent runs **in-process** via [kronk](https://github.com/ardanlabs/kronk) — no separate daemon, no cloud — and can also talk to any OpenAI-compatible endpoint (ollama, llama-server, vLLM) over loopback.

```sh
./outcrop agent enable                         # interactive: installs kronk's
                                               # llama.cpp libs (~50 MB) and
                                               # downloads the recommended
                                               # default model (~1.9 GB).
./outcrop agent status                         # config + model file + libs probe
./outcrop agent test https://example.com "Some Title"   # one-shot prompt + result
```

Or point it at a model you already have, or at an existing local backend:

```sh
./outcrop agent enable --backend kronk --model /path/to/your/model.gguf
./outcrop agent enable --backend http  --model llama3.2:3b              # ollama, etc.
```

The full design and trade-offs are in [RFD 0005](docs/rfd/0005-local-llm-vault-recommendation.md).

### Training data (optional)

Every successful clip can be recorded as a labelled `(input → chosen vault)` row, locally, for later fine-tuning of the agent on *your* routing preferences. Off by default; enabling shows exactly what gets captured before turning on.

```sh
./outcrop training-data enable
./outcrop training-data status     # row count + last capture
./outcrop training-data disable
```

Stored alongside the rest of outcrop's config. Outcrop never uploads anything; export tooling for the recorded rows is deferred ([RFD 0011](docs/rfd/0011-training-data-capture.md)).

## Firefox extension

Prerequisites: Node ≥ 20.

```sh
cd extension/firefox
npm install
npm run build          # → dist/
```

Load the extension via Firefox's `about:debugging#/runtime/this-firefox` → **Load Temporary Add-on…** → pick `extension/firefox/dist/manifest.json`. The options page opens automatically on first install — paste the token from `outcrop init`, click **Test connection**.

Capture flow: click the toolbar icon → pick a vault → **Capture** → drag a rectangle → type notes → **⌘/Ctrl+Enter** to save (or **Escape** to cancel).

`npm run package` produces a loadable `.xpi` in `dist-artifacts/`. See [`extension/firefox/README.md`](extension/firefox/README.md) for development details (`npm run dev`, etc.).

## Project layout

```
.
├── cmd/outcrop/        # CLI entrypoint
├── cli/                # init / serve / vault / config / agent / training-data subcommands
├── server/             # HTTP handlers, middleware, CORS, auth
├── store/              # SQLite schema, migrations, vault / history / meta / training accessors
├── vault/              # vault path resolution, atomic + exclusive writes
├── clip/               # write-a-clip orchestration (decode PNG + compose markdown)
├── agent/              # vault Scorer (history + LLM); kronk + HTTP backends
├── extension/firefox/  # Firefox MV3 extension (TypeScript + esbuild + web-ext)
├── docs/rfd/           # design RFDs
├── vendor/             # vendored Go dependencies
└── go.mod
```

The Go packages live at the root rather than under `internal/` — outcrop isn't published as a library, so the import-prevention `internal/` provides isn't earning its keep here.

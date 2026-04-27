---
rfd: 0005
title: Local-LLM Vault Recommendation
status: accepted
created: 2026-04-27
authors:
  - gdey
tags: [design, server, llm, agent, go]
related: [0001, 0003, 0004, 0006, 0007, 0008, 0009, 0010, 0011]
superseded-by: ~
---

# Local-LLM Vault Recommendation

## Summary

Two LLM-augmented modes for vault routing, both fully local, both opt-in:

1. **Pre-clip ranking** — at popup-open time, the LLM looks at the page URL, page title, and vault display names, and nudges the most likely vault to the head of `GET /vaults`. The v1 heuristic (most-recently-used per registrable domain) stays as the fallback.
2. **Auto-route at capture time** — the user can pick a synthetic "Auto" entry in the vault picker. On Save, the LLM looks at the full clip context (cropped image, notes, selected text, URL, title) and decides which vault to write to. The extension surfaces this as a normal vault choice; the routing happens server-side.

This is the agent contract that RFD 0001 reserved a seam for, and that RFD 0003 punted on under "Deferred concerns".

## Motivation

The v1 heuristic is thin:

- New domain → no history → falls back to alphabetical by vault name with the configured default flagged.
- Long-tail browsing (most reading is across many domains, not a few sticky ones) means the heuristic rarely earns its keep.
- The user's vault names ("Work Notes", "Personal", "Recipes", "Reading List") encode intent. A small LLM can read URL + title + those names and pick correctly far more often than recency-by-domain ever will.

The extension already shows a top-ranked vault as a "pill" with the rest expandable (RFD 0001 §"Vault selection UI"). Better top-1 selection is exactly the win.

## Goals

- Smarter top-1 vault on `GET /vaults` for pages with no history.
- Opt-in "Auto" mode at capture time that delegates the routing decision to the LLM with full context (image + notes + URL + title).
- Fully local: no page content leaves the user's machine.
- Optional: outcrop works as it does today when no LLM is configured.
- No cgo in the outcrop binary. Single-static-binary property from RFD 0003 stays.
- Extension contract minimally extended — one synthetic vault entry at the top of the picker, one boolean on `POST /clip`. No new endpoints, no message-shape changes elsewhere.

## Non-goals (this RFD)

- **Post-clip enrichment.** Tag suggestions, TL;DR injection, body rewrite, smarter titles. The capture-time work here is *routing only* — Auto mode picks a vault, it does not modify the markdown.
- Cloud LLM fallback.
- A bespoke embeddings store. We use chat completions only.
- Silent override of the user's explicit vault choice. Auto-route is opt-in *per-capture*; the user actively picks "Auto" — never inferred.

## Where this fits in the architecture

RFD 0001 §"Routing policy lives on the server" already names the seam:

> "A future local agent (see below) plugs in by replacing the server's scorer. The extension never changes."

This RFD turns that seam into a concrete `Scorer` interface, gives it two implementations (history-only and LLM-augmented), and wires it into `server/vaults.go`.

```
                      ┌────────────────────────────────────┐
GET /vaults?url=&… ─▶ │ server.handleListVaults            │
                      │   1. ListVaults                    │
                      │   2. Scorer.Score(url, title, vs)  │
                      │   3. compose response              │
                      └─────────────────┬──────────────────┘
                                        │ Scorer interface
              ┌─────────────────────────┴─────────────────────────┐
              ▼                                                   ▼
   ┌───────────────────────┐                       ┌──────────────────────────┐
   │ HistoryScorer         │                       │ LLMScorer                │
   │ (RFD 0003 behaviour:  │                       │ (this RFD)               │
   │ MRU per registrable   │                       │ - HistoryScorer first    │
   │ domain → alphabetical)│                       │ - then ask LLM for top-1 │
   └───────────────────────┘                       │ - move it to the head    │
                                                   │ - on any failure, return │
                                                   │   HistoryScorer's result │
                                                   └──────────────────────────┘
```

The `LLMScorer` *wraps* `HistoryScorer` rather than replacing it: the LLM gets to nudge the top of the list, but the rest of the order — and the safety net when the LLM is unavailable — is still the v1 heuristic. The user's experience with the agent off is identical to today.

### Scorer interface

```go
package agent

// Scorer ranks a vault list against the page being clipped. The input order is
// not meaningful; the output order ranks first→best.
type Scorer interface {
    Score(ctx context.Context, in Input, vaults []store.Vault) []store.Vault
}

// Input is what the popup gives us when it opens.
type Input struct {
    URL   string
    Title string
}
```

The signature is intentionally small: no error return, because the contract is that a Scorer always produces *some* ordering. Internal failures (LLM timeout, etc.) degrade silently to the underlying heuristic. The handler doesn't need to know which scorer it has.

## Transport choice

Two viable options, both fully local:

1. **In-process via the kronk SDK.** kronk binds llama.cpp through [`yzma`](https://github.com/ardanlabs/yzma), which uses purego + libffi to load the shared library at runtime — **no cgo at build time**. `GOOS=windows go build` still works, the outcrop binary still cross-compiles, the "no cgo / single static binary" property from RFD 0003 is preserved. The runtime does need libllama on the system; yzma's loader handles platform-specific lookup.
2. **Out-of-process via HTTP.** Outcrop talks to a separate local LLM server using the OpenAI chat-completions shape (`POST /v1/chat/completions`). Works with ollama, llama.cpp's `llama-server`, `kronk server start`, vLLM, LM Studio, etc.

The `Scorer` interface above doesn't care which one is in play — the choice is made at construction time. Both impls live behind the same seam.

### Recommendation: kronk SDK as the default; HTTP-compatible as the alternative

The instinct to recommend HTTP only was based on a wrong reading of the cgo question — `yzma` is purego + ffi, not cgo, so the "no cgo" constraint from RFD 0003 doesn't rule out the SDK. With that off the table, the user-experience argument for in-process wins:

- **One command sets it up.** `outcrop agent enable --model path/to/model.gguf` and the next `outcrop serve` brings the LLM up alongside the HTTP server. No "first install ollama, pull a model, leave it running" ceremony.
- **One process to operate.** No second daemon to start, monitor, restart, or update. The agent's lifecycle is the server's lifecycle.
- **One configuration surface.** The user picks one model in one place; `outcrop` owns the wiring.
- **Aligned with project ethos.** RFD 0001's tagline is "no cloud, no account, on the user's machine." Making the LLM part of outcrop rather than something to install separately reinforces that.

The HTTP path stays supported because:

- Users who already have ollama running for other tools shouldn't have to load a second copy of the same model into kronk.
- Users with niche backends (vLLM, mlx, LM Studio, a hosted local-network endpoint) need a way in.
- It's the way to use `kronk server start` for users who prefer process isolation.

### What kronk-as-SDK adds to the binary

- `github.com/ardanlabs/kronk/sdk/kronk` and its transitive deps (notably `yzma`). Both pure Go.
- A runtime requirement on libllama. yzma handles platform-specific location and loading; we surface a clear error from `outcrop agent status` if the library isn't found.
- Memory footprint when the agent is enabled: the model's working set, dwarfing everything else outcrop holds. With the agent disabled, no model is loaded and no LLM code is exercised.

### Model lifecycle (in-process)

`kronk.New(...)` is invoked lazily on first scorer call, then cached for the server's lifetime. `Unload` is called on `outcrop serve` shutdown. v1 doesn't try to be clever about idle eviction; if the server is running and the agent is enabled, the model is resident.

For the HTTP path the lifecycle belongs to whatever backend the user is running.

### Why not subprocess management

Spawning kronk-server (or ollama) as a child of `outcrop serve` was considered as a third option — the "all-in-one" experience without the in-process memory cost. It's not the right call: model files are large, startup is slow, and child-process lifecycle (restart on crash, log capture, port allocation) is fiddly. The two clean answers are "in our process" and "in someone else's process." Don't half-do it.

## Recommendation flow

`GET /vaults?url=&title=` today:

1. `ListVaults` from the store.
2. `VaultKeysForDomain(registrableDomain(url))` from history.
3. `rankVaults(vaults, historyKeys)` (RFD 0003).
4. Set `isDefault` on whichever vault matches `meta.default_vault_key`.

With the LLM scorer wrapping the heuristic:

0. *(new)* If `agent.enabled` and `url`/`title` present, race two operations:
   - History ranking (cheap, in-process).
   - LLM suggestion call (network, bounded by `agent.timeout_ms`).
1. If the LLM responds in time with a vault name that matches one of the user's vaults, move that vault to the head of the history-ranked list.
2. Otherwise, return the history-ranked list unchanged.
3. Set `isDefault` as before.

The LLM call and the history call run in parallel; the history result is always available by the time we decide. The LLM is purely additive — it can promote a vault, never demote, never remove.

### Prompt shape

```
System:
You route a webpage clip to one of the user's notebooks based on the page's
likely topic and purpose. Reply with ONLY the notebook name, exactly as listed.
If unsure, reply UNSURE.

User:
URL: https://example.com/article
Title: How to make sourdough

Notebooks:
- Work Notes — meeting notes, project planning at work
- Personal — journaling, life admin, things to remember
- Recipes — cooking, food, kitchen experiments
- Reading List

Best match:
```

Vault rows with empty descriptions are rendered as just the display name (see "Schema and vault descriptions" above for the formatter rule).

Server-side response handling:

- Trim, lowercase, strip trailing punctuation.
- Exact-match against vault display names (case-insensitive).
- If the response matches one vault → use it.
- If the response is `UNSURE`, empty, or matches zero / multiple vaults → drop the suggestion.

`UNSURE` exists so the LLM can opt out cleanly when nothing fits, instead of hallucinating. We trust it more when it abstains than when it forces a guess.

### Timeout and fallback

- Hard timeout: 1000 ms by default, configurable.
- The popup shows a brief loading state on `GET /vaults`; the timeout has to stay tight enough not to be felt.
- On timeout, the in-flight LLM request is cancelled (context cancellation). The history-ranked list is returned.
- On *any* error path (network failure, non-200, malformed JSON, unparseable response, name not in vault list) the LLM suggestion is dropped silently. The popup never sees an error caused by the agent.
- WARN-level log line per failure with category, sufficient for the user to spot misconfiguration with `outcrop config show`.

### Caching (deferred)

Repeated `GET /vaults` calls for the same `(url, title, vault-set)` triple don't need to re-prompt. A small LRU cache on `(canonicalised-url, sorted-vault-names)` with a short TTL (10 minutes) would eliminate the latency on the second-and-subsequent popup opens for the same page. Out of scope for this RFD but cheap to add later.

## Configuration

`meta` keys — only those relevant to the chosen backend are read:

| Key | Default | Used by | Notes |
|---|---|---|---|
| `agent_enabled` | `"false"` | both | `"true"` / `"false"`. Stored as text to keep `meta` schema unchanged. |
| `agent_backend` | `"kronk"` | both | `"kronk"` (in-process) or `"http"`. |
| `agent_model_path` | `""` | kronk | Absolute path to a GGUF file. Required when `backend=kronk`. |
| `agent_endpoint` | `"http://127.0.0.1:11434/v1"` | http | Ollama default. Path includes `/v1`. |
| `agent_model` | `""` | http | Model name on the endpoint, e.g. `"llama3.2:3b"`. Required when `backend=http`. |
| `agent_api_key` | `""` | http | Optional. Sent as `Authorization: Bearer …` if non-empty. Local backends ignore. |
| `agent_timeout_ms` | `"1000"` | both | Integer milliseconds for the LLM call. |

Stored in `meta` rather than a new table — six singletons, schema fits.

### CLI

```
outcrop agent enable  [--backend kronk|http]
                      [--model PATH-OR-NAME]    # GGUF path for kronk, model name for http
                      [--endpoint URL]          # http only
                      [--timeout MS]
                      [--download]              # kronk: skip the prompt, fetch the default
outcrop agent disable
outcrop agent download [--vision] [--force]    # see "Model bundling and download"
outcrop agent status                            # prints config; if enabled, probes the backend
outcrop agent test <url> [<title>]              # one-shot — runs the prompt, prints the suggestion
```

`enable` infers `--backend` from which of `--model PATH` / `--endpoint URL` is supplied. Defaults to `kronk` per the recommendation above. When backend is kronk and no model is configured, `enable` prompts to download the recommended model unless `--download` is given (skip prompt, just do it) or `--model PATH` is given (use a local file the user already has).

`status` for kronk reports model load state and libllama discovery; for http it pings `/v1/models` (or `/healthz` if the endpoint exposes it). `test` is the debugging surface for prompt tuning. Neither is on the hot path.

## Privacy

The LLM call sends:

- The page **URL**.
- The page **title**.
- The user's **vault display names**.

Nothing else. The image bytes, selected text, and notes (the things RFD 0003 §"Logging" is most defensive about) never reach the LLM, because the LLM is consulted at popup-open time, before any of them exist.

By default the endpoint is `127.0.0.1:11434` — strictly local. If the user points `agent_endpoint` at a non-loopback host, that's their explicit choice; the CLI prints a one-line warning at config time but doesn't refuse.

## Failure modes

| Mode | Behaviour |
|---|---|
| `agent_enabled = false` | LLMScorer is not constructed; HistoryScorer runs alone. |
| Endpoint unreachable | Log WARN; HistoryScorer result returned. |
| Timeout | Context cancelled; HistoryScorer result returned. |
| Non-200 from endpoint | Log WARN with status code; HistoryScorer result returned. |
| Malformed JSON | Log WARN; HistoryScorer result returned. |
| Response doesn't match any vault | Drop suggestion; HistoryScorer result returned. |
| Response is `UNSURE` | Drop suggestion (silent — this is the LLM's correct behaviour, not a failure). |
| LLM picks a vault that is *also* the history-top | No-op — vault is already first. |

The popup never sees an HTTP error caused by the agent. The agent can only make `GET /vaults` better, never worse.

## Model bundling and download

Outcrop ships *without* a bundled model. Including a multi-GB GGUF in the binary would push the distribution from <20 MB to several GB, and most users who never enable the agent shouldn't pay that cost. Instead, the binary knows about a small list of recommended models and can fetch one on demand.

### Recommended models

A short curated table lives in code (`agent/recommended.go`), with entries for both modes:

- **Text-only (default).** Small instruction-tuned model adequate for the pre-clip Suggester. Target ~2 GB so a fresh-install download is a few minutes on a normal connection.
- **Vision-capable.** Bigger (~4–7 GB). Required for Auto-route to actually use the image; without it, Auto-route degrades to prose-only.

The table holds, per entry: human-readable name, source URL (public HTTPS — Hugging Face Hub by default), file size in bytes, expected SHA256, and a brief one-line description for the CLI to print before downloading. Updating the table is a code change; users who want a model not on the list point at it explicitly via `--model PATH`.

### Storage

Models live alongside the DB, in a sibling directory:

```
$XDG_CONFIG_HOME/outcrop/models/<filename>.gguf      # Linux
~/Library/Application Support/outcrop/models/...     # macOS
%AppData%/outcrop/models/...                         # Windows
```

Mirrors the DB location from RFD 0003 §Location. Honors `OUTCROP_MODELS_DIR` as an override (useful for tests; mirrors the `OUTCROP_DB` pattern).

### CLI

```
outcrop agent download [--vision] [--force]
    # Fetches the recommended model if not already present. --vision picks
    # the vision-capable entry. --force re-downloads even if the file exists.
    # Streams to <name>.partial, verifies SHA256, atomic-renames into place.

outcrop agent enable
    # If backend resolves to kronk and no model_path is configured:
    # prints "Download <name> (<size>)? [y/N]" and runs `agent download` on
    # yes. With no TTY, errors out with a hint to run `agent download` first.

outcrop agent enable --download
    # Skip the prompt; download the default text model automatically.
```

`outcrop agent enable --model /path/to/local.gguf` skips the download path entirely — the user is bringing their own.

### Network behaviour

This is the only remote network call outcrop makes. The URL is hard-coded in the recommended-models table; the HTTP client honours `HTTPS_PROXY`. After the file lands and SHA256 matches, no further network traffic occurs. The initial `outcrop init` and any subsequent server work remain entirely local.

### Verification and atomicity

Downloaded bytes stream to `models/.<filename>.partial`. On completion the SHA256 is computed and compared to the table. Match → rename to the final filename. Mismatch → delete the partial and exit non-zero with the computed-vs-expected hashes printed; this is a hard error, not a fall-through. Resuming a partial download is out of scope for v1 (rerun with `--force`).

### Privacy note

The download URL points at a public model host. The request carries the user's IP and a default user-agent. We don't add identifying headers. The download URL ends up in any HTTP-proxy logs on the user's network, same as any other software-update fetch.

## Schema and vault descriptions

The v1 schema (RFD 0003) gets one optional column for free-form context the LLM can use:

```sql
-- store/migrations/00002_vault_description.sql
-- +goose Up
ALTER TABLE vaults ADD COLUMN description TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE vaults DROP COLUMN description;
```

Default is the empty string; every existing row backfills cleanly with no operator action. The column is **server-internal** — it's read by the prompt formatter, not surfaced in the `GET /vaults` response and not shown in the popup. Display names alone work; descriptions just make the LLM better at routing.

`store.Vault` grows a `Description string` field; `CreateVault`, `GetVault`, and `ListVaults` add the column to their SELECT/INSERT lists.

### CLI

```
outcrop vault add  [--default] [--description "..."] <displayName> <path>
outcrop vault describe <key> "<description>"
outcrop vault show <key>             # full row, including description
outcrop vault list                   # truncates description to keep the table narrow
```

`vault show` is added because `vault list` would otherwise be the only read path and it has to truncate. Empty descriptions are normal and not a warning.

### Prompt formatting

Vault list rendered for both `Suggester` (pre-clip) and `Refiner` (Auto-route):

```
Notebooks:
- Work Notes — meeting notes, project planning at work
- Personal — journaling, life admin, things to remember
- Recipes — cooking, food, kitchen experiments
- Reading List
```

Lines without a description omit the em-dash and the trailing text. Mixed populated/empty lists are fine. The response-parsing rules don't change — the LLM still replies with the bare display name (matched case-insensitively against the user's vaults).

## Auto-route mode (capture-time)

The pre-clip ranker runs on URL + title alone — that's all the popup has when it opens. By the time the user clicks Save in the preview card, much more context exists: the cropped image, the notes they just typed, any selected text from the page. An opt-in "Auto" mode hands all of that to the LLM and lets it pick the vault.

This is *not* a silent override. The user explicitly opts in by selecting the "Auto" entry in the vault picker; otherwise their pick is honoured exactly as today.

### UX

When `agent_enabled = true`, the extension's vault picker grows one synthetic entry at the top:

```
🤖 Auto — let the LLM decide
```

Picking it commits to LLM-decided routing for *this clip*. The drag and the preview-with-notes flow proceed as normal. On Save, the extension sends a clip request with `autoRoute: true`. The success notification reads `Saved to <vault> (auto)` so the user always knows where the clip went.

### How the popup knows the agent is enabled

`GET /vaults` is extended to carry a top-level agent flag so the popup gets everything it needs in one trip. The response shape (an array per RFD 0003) becomes a small object:

```json
{
  "vaults": [
    {"key": "01HZX...", "displayName": "Work Notes", "isDefault": false},
    {"key": "01HZX...", "displayName": "Personal",   "isDefault": true}
  ],
  "agent": {
    "enabled": true
  }
}
```

The extension renders the synthetic "Auto" entry iff `agent.enabled` is `true`. The richer agent-capability surface — vision availability, backend version, local-vs-remote, model name — is *not* on `/vaults`; it lives in a separate `GET /agent/status` endpoint scoped to **RFD 0006**.

This is a breaking change to `/vaults`. The extension is the only client and the change ships in lock-step with step 7 below; RFD 0003 has been amended with a status note pointing at this delta.

### Wire change

`POST /clip` request grows one optional field:

| Field | Type | Notes |
|---|---|---|
| `vault` | string | Required when `autoRoute` is `false` or absent. Empty when `autoRoute` is true. |
| `autoRoute` | bool, optional, default false | When true, the server picks the vault. |

Server validates that exactly one of `(vault non-empty, autoRoute=true)` holds; otherwise returns `400 bad_request` with code `bad_request`.

`POST /clip` response grows three fields, present on every response so the extension can render a uniform notification regardless of mode:

| Field | Type | Notes |
|---|---|---|
| `vaultKey` | string | The vault that was actually written to. |
| `vaultName` | string | Display name of that vault. |
| `autoRouted` | bool | True iff the LLM made the routing decision. |

The existing `notePath` / `imagePath` fields are unchanged.

### Refiner interface

Vision-aware companion to `Suggester`:

```go
// Refiner picks a vault from the full clip context. Returns the vault display
// name to use, or "" for "unsure" / fall back to the default vault.
type Refiner interface {
    Refine(ctx context.Context, in RefineInput, vaultNames []string) string
}

type RefineInput struct {
    URL          string
    Title        string
    SelectedText string
    Notes        string
    ImagePNG     []byte // raw PNG; backends format per their wire shape
}
```

Both backends implement `Refiner`. The HTTP backend uses OpenAI's vision content-block format (base64-encoded image inside a `content` array). The kronk SDK backend passes the image when the loaded model is vision-capable; if it's text-only the implementation drops the image and uses prose alone — accuracy suffers but the call still works. Picking a vision-capable model is on the user; documentation makes that explicit.

### Prompt sketch

```
System:
Pick the best notebook for this clip from the list. Reply with ONLY the
notebook name. If unsure, reply UNSURE.

User:
URL: https://example.com/article
Title: How to make sourdough

[image]

Notes from the user:
trying this over the weekend

Selected text:
"Bake at 220°C for 30 minutes."

Notebooks:
- Personal — journaling, life admin, things to remember
- Recipes — cooking, food, kitchen experiments
- Reading List
- Work Notes — meeting notes, project planning at work

Best match:
```

Same parsing as pre-clip Suggester: case-insensitive exact match against vault display names; UNSURE / unknown / multi-match → drop.

### Failure / fallback

The user explicitly chose Auto, so the server should never bubble out as an error — it should always pick *some* vault the user has implicitly pre-approved for this page. The fallback is a three-tier chain, tried in order until one picks:

1. **Refiner** — the LLM with full context. First choice.
2. **Default vault** — if Refiner fails (unreachable, timeout, non-200, UNSURE, unparseable, names a missing vault), write to `default_vault_key`.
3. **Scorer ranking** — if no default vault is configured, fall through to the same logic that would have ranked the popup's vault list for this URL + title: the configured `Scorer` (LLM Suggester nudge → `HistoryScorer` MRU per registrable domain → alphabetical). Pick the top.

In other words: the Auto-route fallback is "whatever vault the popup would have suggested as the top-ranked entry for this page." That vault is, by construction, the best non-LLM-vision answer outcrop already has.

The `Scorer` invoked at tier 3 is the *same* one used for `GET /vaults`, so its own internal LLM/history degradation applies. If the Suggester is broken too (the most likely cause of Refiner failing), `LLMScorer` already falls through to `HistoryScorer` — at most one extra timeout window in the fallback path, never a hard error.

| Step that picked the vault | Notification |
|---|---|
| Refiner | `Saved to <vault> (auto)` |
| Default-vault fallback | `Saved to <vault> (auto, fallback)` |
| Scorer-ranking fallback | `Saved to <vault> (auto, fallback)` |

The two fallback cases collapse to the same toast — the user doesn't need to tell them apart at glance time. Server logs (WARN) distinguish them for debugging.

The chain only fails if there are *zero* vaults configured, which is the same precondition that prevents any clip from being saved at all. That case returns the existing `400 bad_request` and is handled by the extension's existing not-configured branch — there's no Auto-specific error to surface.

### Privacy

Auto-route widens what reaches the LLM versus pre-clip:

- Image bytes (cropped PNG).
- The notes the user just typed.
- The selected text.
- URL, title, vault names (also in pre-clip).

Defence:

- Default endpoint is loopback-local; nothing leaves the machine.
- The image and notes were headed for disk anyway; sending them to a local LLM adds no exfiltration risk over baseline.
- Auto-route is opt-in per-capture — the user actively picks it.
- Pointing `agent_endpoint` at a non-loopback host triggers the existing config-time warning. The warning text widens to mention "image and notes" so users who configure a remote endpoint understand what Auto sends.

## Code shape

New package:

```
agent/
├── agent.go         # Scorer + Suggester + Refiner interfaces, Input/RefineInput types
├── history.go       # HistoryScorer (lifted from server/vaults.go's rankVaults)
├── llm.go           # LLMScorer — wraps an inner Scorer + a Suggester (pre-clip)
├── route.go         # AutoRouter — picks a vault via Refiner with default-vault fallback
├── prompt.go        # prompt templates + response parsing (backend-agnostic)
├── http_backend.go  # Suggester + Refiner over OpenAI chat.completions (text + vision)
├── kronk_backend.go # Suggester + Refiner over the kronk SDK
└── *_test.go        # table-driven, with fake Suggester / Refiner
```

The LLM-call surface is split into two small interfaces so merge logic and routing logic are each tested independently of either backend:

```go
// Suggester picks a vault for the pre-clip ranker. URL + title context only.
type Suggester interface {
    Suggest(ctx context.Context, in Input, vaultNames []string) string
}

// Refiner picks a vault for Auto-route. Full clip context including the image.
type Refiner interface {
    Refine(ctx context.Context, in RefineInput, vaultNames []string) string
}
```

`LLMScorer` composes a Suggester with an inner Scorer (pre-clip merge). `AutoRouter` composes a Refiner with the store's default-vault lookup (capture-time fallback). Both backends — `http_backend.go` and `kronk_backend.go` — implement *both* interfaces; the same model serves both modes when it's vision-capable.

`server/vaults.go` shrinks: `rankVaults` and `registrableDomain` move into `agent/history.go`. The HTTP handler becomes:

```go
func (s *Server) handleListVaults(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    vaults, err := s.store.ListVaults(ctx); …
    defaultKey, err := s.store.Meta(ctx, store.MetaDefaultVaultKey); …
    ranked := s.scorer.Score(ctx, agent.Input{
        URL:   r.URL.Query().Get("url"),
        Title: r.URL.Query().Get("title"),
    }, vaults)
    …writeJSON…
}
```

The `s.scorer` and `s.router` fields are built at `server.New` time:

- `agent_enabled = false` → `s.scorer = HistoryScorer{...}`, `s.router = nil`. `POST /clip` with `autoRoute=true` returns `400 bad_request`.
- `agent_enabled = true` → `s.scorer = LLMScorer{inner: HistoryScorer{...}, suggester, timeout}` and `s.router = AutoRouter{refiner, scorer: s.scorer, store: s.store, timeout}`. The same backend instance (kronk SDK or HTTP client) implements both `Suggester` and `Refiner`. `AutoRouter` holds a reference to the same `s.scorer` so its tier-3 fallback walks the popup's exact ranking.

`server/clip.go`'s `handleClip` dispatches on `req.AutoRoute`: false → resolve `req.Vault` as today; true → call `s.router.Pick(ctx, req)` to get the vault key, then proceed with the existing write path.

Migrations are unchanged — only meta keys are added, and they're optional.

## Build order

1. **Extract.** Move `rankVaults` + `registrableDomain` into `agent/history.go`. `HistoryScorer.Score` calls them. Wire `Scorer` into `server.handleListVaults`. Behaviour is identical to today; this is the seam landing.
2. **Schema + vault CLI.** Migration `00002_vault_description.sql` adds `vault.description`. `store.Vault` struct + accessors updated. New CLI surface: `outcrop vault add --description`, `outcrop vault describe`, `outcrop vault show`. No agent code yet — the column lands ahead of anything that reads it.
3. **Merge logic.** `LLMScorer` + `Suggester` interface. Fake Suggester for tests. Cover: LLM hits a vault in the list, LLM misses, LLM says UNSURE, LLM names a vault that doesn't exist, LLM times out, LLM picks the same vault history would have. No real LLM yet. Prompt formatter consumes `Vault.Description` from the start.
4. **HTTP backend.** `agent/http_backend.go` — chat-completions only, no streaming, single request type. Easier to land first because it's mockable with `httptest`.
5. **Agent config + CLI.** All six `meta` keys + the four `outcrop agent` subcommands. Interim default for `agent_backend` is `"http"` so the CLI is usable. `enable --backend kronk` is accepted but errors with "kronk backend not yet implemented" until step 6.
6. **kronk backend + model download.** `agent/kronk_backend.go` — wires `kronk.New(model.WithModelFiles(...))`, holds the `*Kronk` for the server's lifetime, calls `Chat` with the prompt, parses. `outcrop agent status` now probes libllama and reports the model load state. Also lands: the recommended-models table (`agent/recommended.go`), the `outcrop agent download` command (HTTPS fetch + SHA256 verify + atomic rename), and the first-enable prompt flow. Flip the `agent_backend` default to `"kronk"` in this step; document the transition in the changelog so existing configs (which already have an explicit value) are unaffected.
7. **Auto-route.** `Refiner` interface, `AutoRouter`, and `Refiner` impls on both backends. `POST /clip` request adds `autoRoute`; response adds `vaultKey` / `vaultName` / `autoRouted`. `GET /vaults` response wraps the existing array in `{vaults, agent: {enabled}}` (breaking shape change; extension client updated in lock-step). Extension grows the synthetic "Auto" entry at the top of the vault picker, gated on `agent.enabled`. Notification text updated.
8. **Docs.** README pointer to choosing a backend (kronk SDK default; ollama / llama-server / kronk-server as the alternatives) and to picking a vision-capable model if the user wants Auto. RFD 0001 status note pointing at this RFD as the now-implemented agent contract.

After step 4 the pre-clip ranker is usable against ollama or any OpenAI-compatible endpoint. After step 6 the in-process backend is wired and becomes the default. After step 7 Auto-route lights up and the extension exposes it.

## Open decisions

1. ~~**In-process default vs HTTP default.**~~ **Decided 2026-04-27: in-process kronk is the long-term default.** Build order is unchanged — HTTP backend still lands first because it's simpler to test against `httptest`, and the merge logic is proven against a real backend before the libllama discovery work begins. During the interim window (after step 3, before step 5) the meta default is `"http"` so `outcrop agent enable` without flags works out of the box; step 5 flips the default to `"kronk"` once that backend exists.
2. ~~**Chat completions vs embeddings.**~~ **Decided 2026-04-27: chat completions for v1.** Simpler — one prompt, one short reply, one parse — and works on the smallest practical local models. Embeddings would require infrastructure that isn't in this RFD (vault descriptions, a vector store with rename/re-embed lifecycle, threshold tuning) and is coupled to design questions worth their own discussion. Validate that the LLM-routing hypothesis pays for itself with chat first; if it does and chat-quality proves insufficient, embeddings get their own RFD. See Deferred below.
3. ~~**Surface the suggestion source in the UI?**~~ **Decided 2026-04-27: no — keep the pill the same.** Treat LLM nudges and history-based ordering identically in the popup; don't add a "suggested by agent" badge. The single-user tool doesn't benefit from the visual noise. Broader questions about surfacing agent state in the UI (capability badges, "this is local" indicators, vision-availability hints) live in **RFD 0006** alongside the capability surface.
4. ~~**Vault descriptions.**~~ **Decided 2026-04-27: add an optional `vault.description` column.** Cheap migration, large prompt-quality lever. Backfills as empty string for existing vaults; no behaviour change for users who don't fill it in. See "Schema and vault descriptions" below.
5. ~~**Default model when none set.**~~ **Decided 2026-04-27: ship with the ability to download an opinionated default; don't embed the model in the binary.** Embedding would balloon the distribution from <20 MB to multiple GB and force the cost on users who never enable the agent. Instead, `outcrop agent download` fetches a recommended model on demand (HTTPS + SHA256 verify), and `outcrop agent enable` prompts to run it on first use. Users who want a different model point at one explicitly with `--model`. See "Model bundling and download" below.
6. ~~**Vision-capability detection for Auto.**~~ **Decided 2026-04-27: always-show in v1.** When the agent is enabled, the "Auto" entry is rendered regardless of the model's capabilities; on a text-only model it gracefully degrades to prose-only routing. The richer story — declaring backend capabilities, gating Auto on vision availability, surfacing the model's actual capability set to the extension — is its own design discussion and lives in **RFD 0006**.
7. ~~**How the extension learns the agent is enabled.**~~ **Decided 2026-04-27: both, in two layers.** Extend `GET /vaults` with a top-level `agent.enabled` flag so the popup gets everything it needs in one trip — that's all v1 needs. A richer `GET /agent/status` endpoint (capabilities, version, locality, model name) is **scoped to RFD 0006**, not built here.

## Deferred (future RFDs)

Each item here has been spun out to its own `ideation` RFD that captures the topic and rough scope. None is under active development; any of them can be picked up and moved to `draft` when the motivating evidence exists.

- **[RFD 0007 — Capture-time Enrichment](0007-capture-time-enrichment.md).** After the clip is on disk, an LLM pass that proposes tags / a TL;DR / a smarter title and rewrites the markdown. Distinct from Auto-route — enrichment never re-routes.
- **[RFD 0008 — Embedding-based Vault Routing](0008-embedding-based-vault-routing.md).** Replace (or augment) chat-completions ranking with cosine similarity over vault-description and page embeddings. Lower latency, more deterministic, smaller model, but pulls in a vector store with lifecycle hooks. Worth its own RFD once chat-rank quality or latency is the bottleneck.
- **[RFD 0009 — Embedding-based Per-vault Clip Retrieval](0009-embedding-based-clip-retrieval.md).** Read-side semantic search across saved clips. Different problem from routing; depends on RFD 0008's embedding lane.
- **[RFD 0010 — Multi-shot / Agentic Flows](0010-multi-shot-agentic-flows.md).** The LLM as orchestrator over multiple coordinated calls — fetch page text, summarise, route + enrich in one chain. Way out on the horizon; opens once single-shot's quality plateaus.
- **[RFD 0011 — Training-data Capture](0011-training-data-capture.md).** Record each clip as a training example: page context, candidate vaults, the LLM's suggestion, what the user actually picked. Opt-in, local-only, exported via `outcrop training-data export` for an external fine-tune workflow. Lets the agent learn *this user's* routing preferences over time.

## Status notes

- 2026-04-27 — Created as `draft`. Foreshadowed by RFD 0001 §"Future: local agent integration" and RFD 0003 §"Deferred concerns".
- 2026-04-27 — Corrected the analysis of kronk: yzma uses purego + libffi, not cgo, so the "no cgo" property from RFD 0003 is preserved by the SDK. The original draft recommended HTTP-only on a wrong reading of that question. Revised to recommend the kronk SDK as the v1 default with HTTP as the supported alternative, and split `Suggester` out of `LLMScorer` so the two transports share the merge logic.
- 2026-04-27 — Settled open decision #1: in-process kronk is the long-term default; HTTP lands first as the implementation-order choice (simpler to test, validates the merge logic before libllama discovery work). Default `agent_backend` is `"http"` between steps 3 and 5 of the build order, then flips to `"kronk"` at step 5.
- 2026-04-27 — Added Auto-route mode (opt-in, capture-time): a synthetic "Auto" entry in the vault picker that delegates the routing decision to the LLM with full context (image + notes + URL + title). New `Refiner` interface alongside `Suggester`; `POST /clip` request adds an `autoRoute` bool, response adds `vaultKey` / `vaultName` / `autoRouted`. New build-order step 6 covers the wire change and the extension UI; step 7 (was 6) is now docs. New open decisions #6 (vision-capability detection) and #7 (how the extension learns the agent is enabled).
- 2026-04-27 — Refined Auto-route fallback to a three-tier chain: Refiner → default vault → Scorer-ranking ("the popup's top entry"). Removed the `auto_route_no_default` 400 path: with no default vault configured, fall through to the Scorer instead of erroring. The fallback never errors as long as at least one vault exists. `AutoRouter` now holds a reference to the configured `Scorer` for tier 3.
- 2026-04-27 — Steps 1–5 of the build order shipped. Promoted `draft` → `accepted`. What landed:
  - Step 1: `agent/` package, `Scorer` interface, `HistoryScorer`. Pure refactor.
  - Step 2: migration `00002_vault_description.sql` and the `vault add --description` / `vault describe` / `vault show` CLI surface.
  - Step 3: `Suggester` interface, `LLMScorer`, `BuildSuggestPrompt` / `ParseSuggestResponse`. The parser is a *longest-prefix* match (with non-alphanumeric-or-EOS boundary) rather than the strict-equality match originally specified — a real-world hardening: small models reliably echo `"Reading List — ML papers"` back as their answer, and we'd rather match the prefix than fall through on UNSURE.
  - Step 4: `agent.HTTPSuggester` over OpenAI-compatible chat-completions, with `SuggestVerbose` returning the raw model reply alongside the parse for use by `outcrop agent test`. `agent.CheckEndpoint` probes `/v1/models` for `outcrop agent status`.
  - Step 5: seven `meta` keys, `outcrop agent enable/disable/status/test`, `cli.buildScorer` wired into `cli/serve.go`. `server.New` grew a `Scorer` parameter (nil → default `HistoryScorer`).
  - Smoke-tested end-to-end against ollama. Cold-load latency for a 3B model on first call exceeds the 1s production budget — pre-warming or a longer default budget needs to land before this is friendly to the popup. Caught and noted; not blocking.
  - Observed: 3B-class models are weak at abstaining (UNSURE) even with a hardened prompt — the system reliably routes to the right vault when one fits, but small models force a match when none does. Documented as a known v1 limit; the right answer is either a 7B+ model or fine-tuning per RFD 0011. Architecture is sound.
- 2026-04-27 — Considered dropping `autoRoute` and using empty/absent `vault` as the auto-route signal. Rejected: an empty `vault` is already a `400 bad_request` per RFD 0003 §POST /clip, so overloading it would give the same wire payload two meanings depending on whether the agent is enabled — the kind of context-dependent semantics that bites later when an agent flag is forgotten. Kept the explicit `autoRoute: bool`. If the wire-shape itch returns, the less-risky compaction is a sentinel *value* on the existing field (`vault: "auto"`) — ULIDs are 26-char Crockford base32 so the literal string `"auto"` can never collide with a real key — rather than empty-means-auto.
- 2026-04-27 — Settled open decision #2: chat completions for v1; embeddings deferred to a future RFD. Chat is the cheapest way to validate that LLM routing pays for itself; embeddings come with their own infrastructure (vault descriptions, vector store, lifecycle hooks, similarity threshold) and deserve independent discussion. Added a Deferred entry naming what that future RFD covers.
- 2026-04-27 — Settled open decision #4: add an optional `vault.description` column. Migration `00002_vault_description.sql` backfills as empty string. Column is server-internal — read by the prompt formatter, not exposed via `GET /vaults` and not surfaced in the popup. CLI gains `vault add --description`, `vault describe`, `vault show`. Prompt formatter joins name and description with " — " when description is non-empty; lines with empty descriptions render as just the name. New build-order step 2 covers the schema work; original steps 2–7 renumber to 3–8. RFD 0003 status note added pointing at this amendment.
- 2026-04-27 — Settled open decision #5: ship with the ability to download an opinionated default model; don't embed it in the binary. New "Model bundling and download" section spells out the recommended-models table, storage location (sibling of the DB), the `outcrop agent download` command, the first-enable prompt flow, and SHA256-verified atomic writes. Build-order step 6 expanded to cover the download infrastructure.
- 2026-04-27 — Settled open decisions #6 and #7. #6: in v1 the "Auto" entry is always shown when the agent is enabled (text-only models degrade gracefully); the richer capability story is **RFD 0006** (`ideation`). #7: extend `GET /vaults` with a top-level `agent.enabled` flag — one trip, all the popup needs. The richer status surface (capabilities, version, local-vs-remote, model name) lives in a future `GET /agent/status` endpoint scoped to RFD 0006. The `/vaults` response shape becomes `{vaults: [...], agent: {enabled}}`, breaking the array shape from RFD 0003; build-order step 7 carries both the server change and the extension client update. RFD 0003 status note added pointing at the wire delta. RFD 0006 added to `related`.
- 2026-04-27 — Settled open decision #3: keep the pill visually identical regardless of whether the LLM nudged the order. No "suggested by agent" badge in v1; broader questions about surfacing agent state in the UI (capability badges, locality indicators) are scoped to RFD 0006. With this, **all seven open decisions are resolved**; the RFD is ready to be promoted from `draft` to `discussion` (or directly to `accepted`) once you're ready to start building.
- 2026-04-27 — Step 5 smoke-test findings (post-description rework). The earlier "3B-class models are weak at abstaining" observation needs an update: the model wasn't weak at abstaining, it was weak at picking a vault it had nothing to score against. With three small concrete findings worth carrying forward:

  - **Empty vault descriptions are a routing penalty for small models, not a neutral baseline.** A vault with no description effectively cannot win against a vault with one, regardless of whether it's named as the default. With the same prompt and fall-back instruction, `llama3.2:3b` forced "Russia-Ukraine peace talks" into `Tech` while `Personal` was un-described. Adding `"life admin, household, news, current events, things to remember"` to `Personal` flipped the routing to `Personal` cleanly (335 ms warm). Implication for the §"Schema and vault descriptions" recommendation: a description is *strongly* recommended for any vault the user wants the agent to consider, not optional in practice. `outcrop vault add` help text should say so, and the README's quick-start example should pass `--description`.

  - **The `Default notebook:` hint pulls less weight than description match.** It earns its keep when *every* description is a poor fit (genuine UNSURE territory), but in close cases descriptions decide the outcome. That's the right behaviour — defaults shouldn't override semantic signal — and confirms the prompt's three-tier guidance (best fit → default → UNSURE) is being honoured in priority order.

  - **3B-class models are viable for routing once descriptions are real.** This shifts the framing for RFD 0011: a future fine-tune doesn't need to compensate for raw model size; it can focus on user-specific routing patterns layered on top of an already-working baseline. RFD 0011 motivation has been updated accordingly.

  - **Pipeline timings observed**: ~335 ms warm round-trip on a 3B model after first-call cold load; ~1 s including model load on the very first call after ollama unload. The 1 s default `agent_timeout_ms` is too tight for cold-start; **a pre-warm on `outcrop serve` startup or a longer default budget should land before step 7 wires the popup to this**, otherwise the first popup-open after ollama's idle-unload (default 5 min) will fall through to history every time. Pre-condition for step 7.

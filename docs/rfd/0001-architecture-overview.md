---
rfd: 0001
title: Outcrop — Architecture Overview
status: discussion
created: 2026-04-26
authors:
  - gdey
tags: [design, architecture, firefox-extension, obsidian]
related: [0002, 0003, 0004, 0005, 0006, 0007, 0008, 0009, 0010, 0011, 0012, 0013, 0014]
superseded-by: ~
---

# Outcrop — Architecture Overview

## Summary

Outcrop is a personal tool for clipping a selected region of a webpage and saving it as a markdown note (with the source URL preserved) into an Obsidian vault. It has two components: a Firefox extension for the in-browser UI, and a local HTTP server that writes into the vault.

## Goals

- One-shot capture: select a region of a page → end up with a markdown note + screenshot in an Obsidian vault, with the source URL preserved.
- No cloud, no account: everything runs on the user's machine.
- The extension is the only frontend in v1. No separate web UI.

## Non-goals (v1)

- Multi-user / hosted deployments.
- Browsers other than Firefox.
- Sync, search, or any reading UI inside Outcrop itself — Obsidian owns that.

## Architecture

```
┌─────────────────────┐                           ┌──────────────────────┐
│ Firefox extension   │   GET /vaults?url=&title= │ Local server         │
│ (thin client)       │ ────────────────────────▶ │ - rank vaults        │
│ - selection overlay │ ◀──────────────────────── │   by relevance       │
│ - captureVisibleTab │   ranked names + default  │ - track per-domain   │
│ - crop via canvas   │                           │   history            │
│ - grab url/title    │   POST /clip              │                      │
│ - render vault list │ ────────────────────────▶ │ - decode image       │
│   server provides   │   (127.0.0.1, shared      │ - write PNG          │
│                     │    secret in header)      │ - write markdown     │
└─────────────────────┘ ◀──────────────────────── │ - record vault used  │
                                                  └──────────┬───────────┘
                                                             │
                                                             ▼
                                                  ┌──────────────────────┐
                                                  │ Obsidian vault(s)    │
                                                  │ (filesystem)         │
                                                  └──────────────────────┘
```

The extension makes two kinds of calls:

- `GET /vaults` — returns a list of `{key, displayName, isDefault}` entries. Optionally accepts `url` and `title` query params; when present, the list is ordered by relevance to that page (e.g., "this domain has historically been clipped to a particular vault"). Without those params, the response is alphabetical by display name, with the `isDefault` flag set on the user's default if one is configured.
- `POST /clip` — the capture itself, sending the vault `key`. The server records which vault was used so future calls to `/vaults` can rank.

### Why a local server, not `obsidian://`

- URI length limits (~32K on Windows, varies elsewhere) make base64-in-URL unreliable for screenshots.
- Browser extensions cannot write to arbitrary filesystem paths; downloads land in the Downloads folder.
- A local server is the cleanest path and leaves room for future features (multi-vault, post-processing, etc.).

### Why HTTP over loopback, not unix sockets or Native Messaging

- **Unix domain sockets** are not available to Firefox WebExtensions. The API surface (`fetch`, `XMLHttpRequest`, `WebSocket`) is network-only; there's no `unix://` URI scheme and no raw socket access.
- **Native Messaging** (`browser.runtime.connectNative()`) is available and avoids ports, CORS, and shared-secret auth — the OS pipe is the security boundary. But Firefox's Native Messaging has a ~1MB per-message limit, which Retina screenshots routinely exceed even when cropped. Chunking is possible but throws away the simplicity. Native Messaging also ties the server's lifetime to the extension session, which makes a CLI (e.g., `outcrop vault add`) unable to talk to the running server.
- HTTP-over-loopback gives us payload headroom, a long-running process the CLI can also talk to, and a transport that works the same on every OS without per-browser manifest dances. The cost is the auth/CORS plumbing already covered above.

### Why request/response, not WebSocket

The extension's calls are discrete user actions — popup opens (`GET /vaults`), user clicks capture (`POST /clip`). Not a stream.

- TCP setup on `127.0.0.1` is tens of microseconds; the image encoding and disk write dominate by orders of magnitude. Connection reuse buys nothing measurable.
- MV3 background scripts behave like service workers and can be terminated when idle. WebSockets from that context are fragile — they drop when the worker stops, and reconnect logic has to be lifecycle-aware. Mozilla's own guidance is to avoid persistent connections from service workers.
- WebSocket earns its keep for server-push, streaming progress, or high event rates. We have none of those in v1.

If we later want server-push (e.g., "vault list changed, please refresh"), Server-Sent Events (SSE) is a simpler fit than WebSocket — unidirectional, plain HTTP, just a response that stays open. Out of scope until we actually need it.

## Capture flow

1. User triggers the extension and drags (or clicks; see open decisions) a selection on the page.
2. Extension calls `browser.tabs.captureVisibleTab()` and crops to the selection on a canvas.
3. Extension reads `tab.url`, `tab.title`, and any user-selected text.
4. Extension POSTs JSON to the local server:

   ```json
   {
     "imageBase64": "...",
     "url": "https://example.com/article",
     "title": "Page title",
     "selectedText": "...",
     "notes": "...",
     "vault": "01HZX9F8K7B2YQE6M4..."
   }
   ```

   `vault` is the stable key returned by `GET /vaults`, not the display name.

5. Server decodes the image, writes the PNG to the vault's attachments folder, writes the markdown note referencing it, returns success (with the path written, for the extension to confirm).

## Note format (sketch)

```markdown
# Clipped from [Page Title](https://example.com/article)

*Captured 2026-04-26 14:32*

![[clip-2026-04-26-143200.png]]

## Notes
```

Filenames are derived from capture timestamp, not page title, to avoid collisions and sanitization headaches. Title still goes in the H1.

## Decisions

### Backend language: **Go**

A single static binary is the cheapest thing to ship across macOS / Linux / Windows. The user already prefers Go. No runtime to install on the target machine. Python and Node were considered and rejected on the deployment-friction axis.

### Server binding: **127.0.0.1 only**

Never `0.0.0.0`. The server is for one user, on one machine. Binding to loopback is the first line of defence against other devices on the LAN.

### Auth: shared secret in a request header

A random token generated on first run, stored alongside the server config, and copied into the extension's settings. Prevents other local processes from POSTing to the vault. The token is required on **every** route, including `GET /vaults` — otherwise any local process can probe whether outcrop is running.

### Vaults are referenced by stable key, not path or display name

Each vault has two identifiers:

- **`key`** — server-assigned at creation, opaque, immutable. ULID (Crockford base32, sortable by creation time). This is what travels on the wire and what history is keyed by.
- **`displayName`** — user-provided, mutable, free-form. Used only for rendering in the picker and settings UI. Does not need to be unique, though duplicates may earn a warning.

The server keeps vault filesystem locations private. `GET /vaults` returns `[{key, displayName, isDefault}, ...]`; `POST /clip` sends `{vault: <key>, ...}`. The extension never sees on-disk paths, and the server resolves `key → path` internally. This means:

- Smaller blast radius if the extension is ever compromised — an attacker who steals the token still doesn't learn where the user's vaults live.
- The server can move vault directories on disk, and the user can rename the display name, without anything else changing. History keyed by `key` survives both.
- The extension is free to cache the `(key, displayName)` pairs without worrying about staleness — the key is the source of truth, the name is just a label.

### CORS

The server must return CORS headers permitting the `moz-extension://` origin so the extension's `fetch()` calls succeed.

### Filename hygiene

When user-supplied strings (titles, notes) end up in filenames, slugify, strip control characters, and cap length. Default is to use the timestamp-based name and keep the title only inside the markdown body.

### Persistence (run on login)

- macOS: `launchd` plist in `~/Library/LaunchAgents/`
- Linux: systemd user unit
- Windows: Task Scheduler entry (or Startup folder fallback)

### Multi-vault support

The server supports more than one vault. One is marked the default and used when the extension does not specify a vault. The extension asks the server for the list of configured vaults and lets the user pick (see *Vault selection UI* below).

### Routing policy lives on the server

The server, not the extension, decides which vault is the best fit for a given page. The extension passes the page's URL and title; the server returns vault names ranked by relevance. The extension is a thin renderer of whatever order the server gives it.

Why server-side:

- The server is the only component with a long-lived view of history (extension storage gets wiped on reinstall and is awkward to sync across machines).
- Rename or remove a vault and the server handles the cleanup; no stale state hiding in the extension.
- A future local agent (see below) plugs in by replacing the server's scorer. The extension never changes.

For v1 the scorer is a simple heuristic — most-recently-used vault for the same registrable domain, falling back to the default. Storage is whatever fits (a small JSON file is fine; SQLite if it grows). Both belong in their own RFDs.

### Vault selection UI: collapsed pill, expandable on click

The capture popup shows a small pill displaying the top-ranked vault's display name. Clicking the pill expands the full ranked list so the user can pick a different one. The top entry is preselected, so the common case is "user does nothing, hits capture, it goes where the server suggests."

Edge case: when only one vault is configured, the pill is rendered disabled (still showing the vault's name, but visibly non-interactive). This communicates "there's nothing to choose between" without hiding the indicator entirely — the user always knows where the clip is going.

### Page-selection UX: drag-rectangle for v1

The capture overlay is a drag-rectangle. Universal mental model (matches every screenshot tool), no DOM-awareness required from the user, and enough to prove the full pipeline end-to-end.

Click-an-element capture is planned as a v1.1 follow-up — see RFD 0002. The server contract doesn't change between the two; the extension just produces the rectangle a different way.

### Vault layout: `Clippings/` with `Clippings/attachments/` for v1

Each vault's clippings live at:

```
<Vault>/Clippings/<note>.md
<Vault>/Clippings/attachments/<image>.png
```

Reasons:

- Keeps the vault's root tidy. Clippings don't intermix with notes the user is writing by hand.
- Attachments stay co-located with the notes that reference them (under `Clippings/`), so the entire clipping output sits in one subtree — easy to back up, archive, or delete.
- Obsidian wikilinks (`![[image.png]]`) resolve fine because Obsidian searches the whole vault by default; the relative location doesn't matter for the link to work.

The flat alternative was rejected: it makes clippings indistinguishable from hand-written notes, which is fine for low volume but gets messy fast.

Per-vault configurable layout is planned as a v1.1 follow-up — different vaults serve different purposes, and a single hard-coded layout will eventually chafe. For v1, hard-coded is enough.

### Future: local agent integration

A locally running agent will eventually be wired in to make smarter routing suggestions (and possibly summarize clips, propose tags, etc.). The contract: the server calls the agent; the extension never does. This keeps the extension thin and keeps the agent off the network. Out of scope for this RFD beyond reserving the seam.

## Build order

1. Server first. Get it writing a hardcoded markdown file from a `curl` POST into a real vault.
2. Extension selection overlay + capture (against a stub server).
3. Wire them together end to end.
4. Persistence (launch-on-login) per OS.

## Out of scope for this RFD

- Concrete HTTP route shapes and error codes — to be written up once the server has a working skeleton.
- Settings file format on disk.
- Token-rotation / revocation flows.
- The scoring algorithm and history-storage format for vault ranking — its own RFD.
- The agent contract (transport, request/response shape, how the server discovers it) — its own RFD when we're ready to build it.

## Status notes

- 2026-04-26 — Created as `discussion`. Architecture decisions settled.
- 2026-04-27 — Server side of the architecture is built per RFD 0003 (now `committed`): all endpoints, CLI, history ranking, and the SQLite-backed config landed; manual end-to-end via `curl` validated. Extension side is being designed in RFD 0004; until that lands and is implemented, the architecture as a whole is partially realised.
- 2026-04-27 — **Agent contract implemented per RFD 0005** (now `committed`). The "future local agent" reserved in §"Future: local agent integration" is now real: in-process kronk SDK + GGUF model download + LLM-augmented `GET /vaults` ranking. Auto-route mode (the second LLM intervention RFD 0005 designed) was deliberately deferred during the smoke-test phase — the pre-clip ranker is sufficient and the cleaner training signal preserved by manual user override is more valuable than in-popup convenience. The architecture diagram still holds; the agent box is filled in.
- 2026-04-27 — Training-data capture (RFD 0011, now `accepted`) ships alongside the agent: every successful clip can record one labelled `(input → chosen vault)` row when `outcrop training-data enable` is run. The dataset is local, opt-in, and powers a future fine-tune of the user's local model on their own routing preferences.

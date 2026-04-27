---
rfd: 0004
title: Firefox Extension Design
status: draft
created: 2026-04-27
authors:
  - gdey
tags: [design, firefox-extension, mv3, typescript]
related: [0001, 0002, 0003, 0005, 0006]
superseded-by: ~
---

# Firefox Extension Design

## Summary

The implementation plan for the Firefox extension half of Outcrop. RFD 0001 specified the extension's role (thin client: selection overlay, `captureVisibleTab`, talk to the local server); RFD 0003 specified the wire contract it talks to. This RFD turns those into concrete decisions about layout, build, manifest, module boundaries, and UX.

## Scope

In scope: Manifest V3 extension layout, module responsibilities, capture flow, settings/options UI, build pipeline, packaging, permissions, error handling.

Out of scope: click-element capture mode (RFD 0002), AMO listing / signed distribution (post-v1), Chrome compatibility (Firefox-only per RFD 0001), telemetry, server auto-discovery.

## Project layout

The Firefox extension lives at `extension/firefox/`, parallel to the Go sources at the repo root. Keeping it inside the same git repo keeps the wire contract (`extension/firefox/src/lib/api.ts` ↔ `server/`) visible to a single PR.

The `firefox` subdirectory leaves room for other browsers (Chrome MV3, Safari Web Extension) without committing to them. Cross-browser is *not* a v1 goal — the directory shape is the only concession we make to it now. If a second browser is ever added, shared code can move to `extension/lib/` at that point; nothing in v1 is built around an abstraction that doesn't exist yet.

```
extension/
└── firefox/
    ├── package.json
    ├── tsconfig.json
    ├── esbuild.config.mjs
    ├── web-ext-config.mjs
    ├── manifest.json             # source of truth, copied verbatim into dist/
    ├── src/
    │   ├── background.ts         # service worker: capture, crop, POST
    │   ├── content.ts            # injected on demand: drag overlay + preview card
    │   ├── popup/
    │   │   ├── popup.html
    │   │   ├── popup.css
    │   │   └── popup.ts          # vault pill, capture button
    │   ├── options/
    │   │   ├── options.html
    │   │   ├── options.css
    │   │   └── options.ts        # server URL + token + test connection
    │   ├── lib/
    │   │   ├── api.ts            # typed wrappers for /healthz, /vaults, /clip
    │   │   ├── settings.ts       # browser.storage.local helpers
    │   │   ├── messages.ts       # typed background ↔ popup ↔ content messages
    │   │   └── crop.ts           # OffscreenCanvas crop helper
    │   └── icons/                # 16/32/48/128 PNGs
    ├── dist/                     # build output, gitignored
    └── README.md
```

A flat `src/` (no `src/components/` etc.) is enough — the extension has four entry points and a few shared library files. Folder hierarchy past that is overhead.

## Language and build

### TypeScript

Type-safety on the wire contract is the main draw. The server's response shapes are nailed down (RFD 0003 §"Endpoints"); mirroring them in TS interfaces means a server-side rename surfaces in the extension at compile time, not at runtime against a real user. esbuild handles `.ts` natively.

### esbuild

Single static binary, fast, native TS, no plugin ecosystem to manage:

- Builds four entry points: `background.ts`, `content.ts`, `popup/popup.ts`, `options/options.ts`.
- Output format: ESM for `background.ts` (MV3 requirement); IIFE for `content.ts` (injected as a single file); ESM for popup and options (loaded as `<script type="module">`).
- Static copy step (a small Node script): `manifest.json`, all `.html` / `.css`, icons → `dist/`.
- Source maps in dev, none in release.

webpack and vite were considered and rejected: too much config and dependency surface for an extension this small.

### web-ext

`web-ext run` for `npm run dev` (live reload + auto-launch a clean Firefox profile against `dist/`). `web-ext build` for `npm run package` (produces an `.xpi` from `dist/`).

### Scripts

```jsonc
// package.json scripts
{
  "build":   "node esbuild.config.mjs && node scripts/copy-static.mjs",
  "dev":     "node esbuild.config.mjs --watch & web-ext run --source-dir=dist",
  "check":   "tsc --noEmit",
  "package": "npm run check && npm run build && web-ext build --source-dir=dist --artifacts-dir=dist-artifacts"
}
```

No linter/formatter dependency for v1 — `tsc --noEmit` is the type gate; format with whatever the editor does.

## Manifest (MV3)

```json
{
  "manifest_version": 3,
  "name": "Outcrop",
  "version": "0.1.0",
  "description": "Clip a region of a webpage into an Obsidian vault.",
  "icons": { "48": "icons/48.png", "128": "icons/128.png" },
  "background": { "scripts": ["background.js"], "type": "module" },
  "action": {
    "default_title": "Outcrop",
    "default_popup": "popup/popup.html",
    "default_icon": { "16": "icons/16.png", "32": "icons/32.png" }
  },
  "options_ui": { "page": "options/options.html", "open_in_tab": true },
  "permissions": ["activeTab", "storage", "scripting", "notifications"],
  "host_permissions": ["http://127.0.0.1:7878/*", "http://localhost:7878/*"],
  "browser_specific_settings": {
    "gecko": { "id": "outcrop@gdey", "strict_min_version": "127.0" }
  }
}
```

### Permissions, justified

| Permission | Why |
|---|---|
| `activeTab` | Read `tab.url` / `tab.title` and call `tabs.captureVisibleTab` on the user's current tab without requiring broad host permission to every site. |
| `storage` | Persist server URL + token in `browser.storage.local`. |
| `scripting` | Programmatically inject `content.js` on capture (avoids declaring `content_scripts` against `<all_urls>`, which would prompt as "this extension can read all your data"). |
| `notifications` | Surface success/failure after the popup has closed (the popup is gone by the time the POST finishes). |

### Host permissions

Default `127.0.0.1:7878` matches RFD 0003's listen address; `localhost:7878` is included because users may type that into settings and it's a different origin from the IP form. The user can override the port in settings, but other ports require manually editing the manifest — acceptable for v1, since `outcrop init` writes `127.0.0.1:7878` by default.

## Module responsibilities

### `background.ts` — service worker

The only module that talks to the server. Owns:

- `tabs.captureVisibleTab()` — needs an active tab; runs in the SW.
- The crop, via OffscreenCanvas (DOM-less, available in MV3 service workers).
- `fetch()` to the local server. Origin is `moz-extension://<uuid>`, which the server's CORS layer reflects (RFD 0003 §"CORS").
- The capture state machine: receives "begin" from popup, holds `{vaultKey, notes, tabId}`, injects content script, awaits a "rect" message, performs the POST, fires a notification.

### `popup/popup.ts` — the toolbar popup

Renders when the user clicks the toolbar icon. Doesn't drive capture — it gathers the vault choice and hands off to background.

- On open: read settings (`serverURL`, `token`), `GET /healthz`, then `GET /vaults?url=&title=` for the active tab. Render the pill (top-ranked vault) plus a collapsed "more vaults" affordance per RFD 0001.
- "Capture" button → `runtime.sendMessage({type: "begin", vaultKey})` → `window.close()`.
- Notes are *not* entered here — they're typed in the preview overlay after the capture completes, so the user can see what they're annotating.
- If `/healthz` fails: red error block with "Open Settings" link; capture button disabled.
- Single-vault edge case: pill is rendered but disabled (no choice to make), per RFD 0001 §"Vault selection UI".

### `content.ts` — selection overlay + preview

Injected on demand by `scripting.executeScript` on the capture flow. Not declared as a content script in the manifest. Two phases of UI, in the same script:

**Phase 1 — drag-rectangle:**

- Captures `window.getSelection().toString()` as soon as it loads (before the overlay covers anything).
- Renders a full-viewport transparent layer (`position: fixed`, max z-index, capturing `pointermove` / `pointerdown` / `pointerup`).
- User drag-rectangle in CSS pixels, then sends `{x, y, w, h, devicePixelRatio, url, title, selectedText}` to background.
- Background performs the crop (see below) and replies with `{imageBase64}`.
- Escape during drag cancels and tears the overlay down without messaging.

**Phase 2 — preview + notes:**

- Replaces the drag overlay with a centred preview card: the cropped image at natural size (capped to viewport), a notes textarea, [Save] and [Cancel] buttons, and a small label showing the destination vault.
- **Rendered inside an `<iframe>`, not directly in the shadow root** — see "Keyboard isolation" below. The iframe's document holds the card's HTML and CSS; all preview event handlers (textarea keydown, button clicks, Escape) are attached inside the iframe's document.
- `Cmd/Ctrl+Enter` saves; `Escape` cancels. Cancel tears down with no POST.
- On Save: sends `{notes}` to background, which performs the POST and fires the success/failure notification. The preview tears down immediately on Save (we don't block on the POST — failure surfaces via notification).

#### Keyboard isolation: the preview is an iframe, not shadow DOM

A closed shadow root gives us *style* isolation, not *event* isolation. Keyboard events fired inside a closed shadow root retarget at the host element and then bubble normally to `document` and `window`, so any page-level keydown listener still sees them. Many sites (YouTube's space-to-play, Twitter's `j`/`k`/`l`, sites that intercept `/` for search, anything using `keydown` capture-phase handlers that call `stopImmediatePropagation` or `preventDefault`) will swallow keystrokes before our textarea sees them — which during smoke testing showed up as "spacebar toggles the YouTube video instead of typing a space in the notes box."

We can't reorder handler registration to preempt the page: the content script is injected via `scripting.executeScript` after page scripts have already registered their listeners, so our document-level capture-phase handlers run *after* theirs. Stopping propagation at the shadow root only catches bubble phase; page capture-phase handlers have already fired by then.

The robust fix is the platform's own isolation boundary: an iframe. Iframe documents are separate browsing contexts; their keyboard events do **not** propagate to the parent document at all. Rendering Phase 2 inside an iframe means the textarea inside the iframe receives every keystroke and the page's listeners never see them. The iframe is appended to the shadow root and is full-viewport, transparent, with `z-index: 2147483647`; its document body is the dimmed backdrop, with the centred preview card inside.

Phase 1 (drag overlay) stays in the shadow DOM. Pointer events don't have this problem — the page's pointer listeners reading our drag-overlay events isn't a meaningful collision, and the only key we care about during phase 1 is Escape, handled by the content script's document-level keydown listener.

### `options/options.ts` — settings page

Two fields: server URL (defaults to `http://127.0.0.1:7878`) and token. Save button persists both. "Test connection" button hits `GET /healthz` with the entered values and shows the result inline. Status bubble (green / red) reflects last-tested state; settings save without testing first if the user prefers.

## Capture flow

```
[Popup]                        [Background SW]                  [Content Script]
  open
  GET /healthz ──────────────▶
  GET /vaults?url=&title= ──▶
  render pill (no notes field)
  user clicks Capture
  sendMessage("begin",
    {vaultKey}) ─────────────▶ remember (tabId → {vaultKey, vaultName})
                               scripting.executeScript(content) ─▶ inject
  popup closes                                                     drag overlay renders
                                                                   user drags
                                                          ◀──── sendMessage("rect", {x,y,w,h,dpr,url,title,selectedText})
                               tabs.captureVisibleTab() → dataURL
                               OffscreenCanvas crop → PNG blob → base64
                               sendMessage("cropped",
                                 {imageBase64, vaultName}) ──▶
                                                                   preview card renders
                                                                     (image + notes + Save/Cancel)
                                                                   user types notes, clicks Save
                                                          ◀──── sendMessage("save", {notes})
                               POST /clip ──────────▶ server
                               notifications.create("Saved to <vault>")
```

The state held by background between "begin" and "save" is `{vaultKey, vaultName, tabId, url, title, selectedText, imageBase64}`. The content script does not retain the cropped image after sending "save" — the preview tears down and the SW owns the bytes through the POST.

### Cropping in a service worker

MV3 service workers have no DOM, so `<canvas>` is unavailable. The crop uses `OffscreenCanvas`:

```ts
async function cropToBase64(dataURL: string, rect: Rect, dpr: number): Promise<string> {
  const blob = await (await fetch(dataURL)).blob();
  const bmp = await createImageBitmap(blob);
  const canvas = new OffscreenCanvas(rect.w * dpr, rect.h * dpr);
  const ctx = canvas.getContext("2d")!;
  ctx.drawImage(bmp, rect.x * dpr, rect.y * dpr, rect.w * dpr, rect.h * dpr,
                     0, 0, rect.w * dpr, rect.h * dpr);
  const out = await canvas.convertToBlob({ type: "image/png" });
  return blobToBase64(out);
}
```

`devicePixelRatio` matters because `captureVisibleTab` returns at the device's pixel density; the rect from the content script is in CSS pixels.

### Service worker lifecycle

Firefox MV3 terminates the SW when idle. The capture flow is no longer sub-second end-to-end — the user is typing notes between "cropped" and "save", which can be many seconds or minutes. The SW could die during the notes-typing window.

Mitigation: keep the in-flight state in `browser.storage.session` (cleared on browser restart, persists across SW restarts). On SW startup, re-attach the `runtime.onMessage` listener; if a "save" message arrives and matching state is present, resume the POST.

For v1 the starting point is `storage.session` — given the human-paced phase 2, in-memory state is too fragile to be the default. The cropped image lives in storage as base64; this costs a few MB while the user types but disappears as soon as Save or Cancel fires (and on tab close, via a lifecycle listener that prunes orphaned entries).

## Settings / storage

`browser.storage.local`:

```ts
type Settings = {
  serverURL: string;  // default "http://127.0.0.1:7878"
  token: string;      // empty until set
};
```

A "configured" extension is one with a non-empty token. The popup checks this on open and routes the user to options if not.

Nothing else lives in storage. History, vault list, and ranking are server-owned (RFD 0001 §"Routing policy lives on the server").

## API client (`lib/api.ts`)

A thin typed wrapper that mirrors RFD 0003's endpoint shapes 1:1.

```ts
export type Vault = { key: string; displayName: string; isDefault: boolean };

export type ClipRequest = {
  vault: string;
  url: string;
  title: string;
  selectedText: string;
  notes: string;
  imageBase64: string;
};

export type ClipResponse = { notePath: string; imagePath: string };

export type ApiError = { error: string; message: string };

export class Client {
  constructor(private baseURL: string, private token: string) {}
  health():  Promise<void>;
  vaults(url?: string, title?: string): Promise<Vault[]>;
  clip(req: ClipRequest): Promise<ClipResponse>;
}
```

All requests carry `Authorization: Bearer ${token}` and `Content-Type: application/json`. Error responses are parsed into `ApiError` and re-thrown as a typed exception so the UI can render `error` codes (RFD 0003 §"Errors").

## UX details

### First-run

On install: open `options.html` in a new tab automatically (`runtime.onInstalled`). Toolbar icon shows a `!` badge when settings are missing. Popup opens to the "not configured" state with a single "Open Settings" button.

### Vault picker pill

Per RFD 0001 §"Vault selection UI: collapsed pill, expandable on click". Top-ranked vault's display name in a small button; click expands a list below it for other vaults. The currently selected vault is the only thing that visually counts as "selected." Default's `isDefault` flag from the API is shown as a small "★" suffix on whichever vault has it, regardless of position.

### Selection overlay style

- Full-viewport semi-transparent backdrop (`rgba(0,0,0,0.25)`), z-index `2147483647`.
- Drag rectangle: 1px solid white border, no fill, slight box-shadow.
- Live size readout in the corner of the rect ("420 × 280"), CSS pixels.
- Cursor: `crosshair`.
- Escape cancels. Click outside the overlay does nothing (only `pointerdown` inside the overlay starts a drag).

### Preview card style

Lives in the iframe's document (see "Keyboard isolation" above), so the styles below are scoped to the iframe — they don't compete with the host page or with the shadow DOM that the drag overlay used.

- The iframe's `<body>` is the dimmed backdrop (`rgba(0,0,0,0.5)`), flex-centred so the card sits in the middle of the viewport.
- The card is a flex column with a fixed background, rounded corners, and a soft shadow.
- Cropped image at natural pixel size, downscaled with `max-width: 90vw` / `max-height: 60vh` so very large captures still fit.
- Below the image: the destination vault name (read-only, as a chip), then a `<textarea>` for notes (auto-focused), then [Cancel] / [Save] buttons aligned right.
- `Cmd/Ctrl+Enter` triggers Save; `Escape` triggers Cancel; clicking the dimmed backdrop does nothing (avoids accidental dismissal of work-in-progress notes).

### Notifications

Success: `Saved to <vault.displayName>` (no path — RFD 0003 §"Logging" treats paths as not-for-logging; same posture in the UI). Failure: `Save failed — <error code>` with a tooltip on hover for the message.

Notifications time out per OS default. Clicking does nothing in v1 (opening Obsidian to the note is a future polish).

### Keyboard shortcut

None in v1. Toolbar click is the only entry. Adding a shortcut means a `commands` manifest entry plus deciding the default chord — defer.

## Error handling

Three layers, each with a defined display:

1. **Settings missing or invalid** → popup routes to options page. No retry, no notification.
2. **Network / `/healthz` fails on popup open** → inline red banner, capture disabled.
3. **`POST /clip` fails after rectangle drawn** → notification with `error` code from the JSON response. The image is gone (we don't retry without the rect). User would need to re-capture.

Server-side errors (`bad_request`, `vault_not_found`, `image_decode_failed`, `disk_write_failed`, `internal`) are surfaced verbatim in the failure notification. We don't translate them — the codes are stable per RFD 0003 and useful for the user reporting issues.

## Testing

For v1, manual smoke tests against a running `outcrop serve`. Automated browser tests for an extension are heavyweight (Playwright + a real Firefox profile + extension loading) and the server already covers the wire contract from its side.

What we *can* unit-test cheaply:

- `lib/crop.ts` — given a known dataURL and rect, the output PNG has the expected dimensions. Runs in Node with `jsdom`-less stubs since OffscreenCanvas isn't in Node — skip for v1; cover in browser tests later.
- `lib/api.ts` — fetch wrapper, mock `globalThis.fetch`, assert headers/body/error parsing. This *is* worth a small Vitest setup if we add one; otherwise punt.

For v1: no test harness in `extension/firefox/`. Lean on `tsc --noEmit` for the type gate plus the manual smoke flow.

## Build, package, distribute

`npm run package` produces a `dist-artifacts/outcrop-0.1.0.zip` (web-ext output). This is the unsigned `.xpi`.

For local install during development: `about:debugging#/runtime/this-firefox` → "Load Temporary Add-on" → pick `dist/manifest.json`. The temporary install survives until Firefox restarts.

Permanent install requires AMO signing or running Firefox Developer/Nightly with `xpinstall.signatures.required = false`. AMO listing is post-v1; for v1 the README documents the temp-install path.

## Open decisions

These are the calls I want to settle before / during implementation:

1. ~~**Notes-after-rectangle, or notes-before?**~~ **Decided 2026-04-27: notes-after.** The user types into the preview card with the cropped image visible above it, so they can annotate what they actually captured. The popup loses its notes textarea; the content script grows a phase-2 preview overlay. Drives the use of `storage.session` for in-flight state (see "Service worker lifecycle" above).
2. **Selected text capture timing.** Read selection in content script before the overlay paints, or read in popup via `tabs.executeScript` before "begin" is fired? The first is simpler (overlay already runs in the page) but means the user can't see "I have your selection" before kicking off a capture. v1: read in content script, surface nothing in the popup.
3. **Single-page captures of `about:` / `view-source:` / extension pages.** `tabs.captureVisibleTab` will fail on these. Surface as a notification, document as a known limit.
4. **Icon design.** Placeholder PNGs for v1; commission proper icons before AMO submission.

## Build order

1. `extension/firefox/` skeleton: `package.json`, `tsconfig.json`, `manifest.json`, `esbuild.config.mjs`, copy-static script. `npm run build` produces a non-functional but loadable `dist/`.
2. Options page end-to-end: enter URL + token, persist, "Test connection" hits `/healthz`. Manually verify against a running `outcrop serve`.
3. Popup opens, calls `/healthz` and `/vaults`, renders the pill (no capture yet).
4. Selection overlay (content script phase 1) standalone — renders, drags, sends a `rect` message that the background just logs.
5. Wire the full round-trip: popup → background (`begin`) → content (drag) → background (`captureVisibleTab` + crop) → content (preview card with notes) → background (`save` → POST `/clip`) → notification. Use `storage.session` for in-flight state from the start.
6. Polish: error states, keyboard cancel/save shortcuts, devicePixelRatio handling, single-vault disabled-pill, options page validation, prune-on-tab-close for orphaned `storage.session` entries.
7. `web-ext build` → loadable `.xpi`. README update with install instructions.

By step 5 the extension is usable end-to-end against a running server. Steps 6–7 are polish and packaging.

## Out of scope (for this RFD)

- Click-element capture mode — RFD 0002.
- AMO listing, signing, auto-update channel.
- Cross-browser compatibility (Chrome MV3 differs in service worker termination semantics and lacks `browser_specific_settings`).
- A "preview before save" step.
- Long-page stitching.
- Editing or replacing the most recent clip from the popup.

## Status notes

- 2026-04-27 — Created as `draft` once the v1 server (RFD 0003) reached `committed`. Move to `discussion` when the design has been read and feedback incorporated; `accepted` once we're ready to scaffold `extension/firefox/`.
- 2026-04-27 — Settled the open decisions on stack (TypeScript + esbuild + web-ext), permissions (`activeTab` + `scripting`), cropping location (service worker via `OffscreenCanvas`), notes timing (after rectangle, in a preview card), and test posture (manual smoke for v1). Notes-after drove a switch from in-memory to `storage.session` for in-flight state to survive long note-typing windows.
- 2026-04-27 — Moved the project root from `extension/` to `extension/firefox/` to leave room for future per-browser variants without committing to them.
- 2026-04-27 — Smoke testing surfaced a keyboard-interception bug: on sites like YouTube the preview-card textarea didn't receive characters that the host page intercepted (spacebar toggled the video instead of typing into the notes box; `j`/`k`/`l` and similar single-key shortcuts had analogous failures elsewhere). Root cause: even with a *closed* shadow root, keyboard events fired inside the shadow retarget at the host and then bubble to `document` / `window`, so any page-level keydown listener still sees them. Page handlers registered before our content script also run first in capture phase and frequently call `stopImmediatePropagation` / `preventDefault`, blocking the textarea entirely. We can't preempt them by registering earlier — content scripts inject after page scripts. **Fix: render Phase 2 (the preview card) inside an `<iframe>` rather than directly in the shadow root.** Iframe documents are separate browsing contexts; keyboard events inside don't propagate to the parent. The textarea now receives every keystroke and the page's listeners never see them. Phase 1 (drag overlay) keeps using the shadow DOM — pointer events don't have this problem. Rationale and pattern are documented in §"Keyboard isolation: the preview is an iframe, not shadow DOM" above.

# Outcrop — Chrome extension

Source for the Chrome (and Chromium-family: Edge, Brave, Arc, etc.) half of [Outcrop](../../README.md). Mirrors the Firefox extension in [`../firefox/`](../firefox/) — same overlay, popup, options page, wire contract — adapted for Chrome's MV3 service-worker background and the `chrome.*` API surface.

## Prerequisites

- Node.js ≥ 20
- A running `outcrop serve` (see the repo root for the Go server)

## Setup

```sh
npm install
npm run gen-icons   # one-shot: generates src/icons/*.png placeholders
```

## Build

```sh
npm run build       # → dist/
npm run check       # tsc --noEmit
npm run package     # → dist-artifacts/outcrop-<version>_chrome.zip
```

## Load it (side-loaded, unpacked)

1. Open `chrome://extensions`.
2. Toggle **Developer mode** (top-right).
3. Click **Load unpacked** and pick this directory's `dist/` folder.
4. The Outcrop options page opens automatically; paste the token printed by `outcrop init` and click **Test connection**.

The extension stays loaded across browser restarts. Re-run `npm run build` after editing source, then click the **Reload** icon on the extension card in `chrome://extensions`.

## Develop

```sh
npm run dev         # esbuild watch + web-ext run --target=chromium
```

`web-ext run --target=chromium` opens a fresh Chromium profile with the extension auto-loaded and reloads it on dist changes — useful for tight iteration without polluting your everyday Chrome profile.

## Distribution

The `package` script produces a plain zip; Chrome's "Load unpacked" doesn't need it, but it's the format you'd upload to the Chrome Web Store if you ever go that route.

# Outcrop — Firefox extension

Source for the Firefox half of [Outcrop](../../README.md). Designed in [RFD 0004](../../docs/rfd/0004-firefox-extension.md); the wire contract is defined in [RFD 0003](../../docs/rfd/0003-v1-server.md).

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
npm run package     # → dist-artifacts/outcrop-<version>.zip (loadable .xpi)
```

## Develop

```sh
npm run dev         # esbuild watch + web-ext run with a clean profile
```

Or load `dist/manifest.json` manually via `about:debugging#/runtime/this-firefox` → "Load Temporary Add-on…". Temporary installs survive until Firefox restarts.

## First-run

After installing, open the Outcrop options page and paste the token printed by `outcrop init`. Click "Test connection" to confirm the local server is reachable.

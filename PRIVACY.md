# Outcrop — Privacy Policy

*Last updated: 2026-04-27.*

Outcrop is a local-first tool. Everything you clip, everything outcrop captures about how you clip, and everything you configure stays on the machine you installed it on. There is no Outcrop-operated server; there is no account; there is no telemetry; there is no automatic updater. This document describes what outcrop captures, where it puts it, and the small handful of cases where data does leave your machine — always at your explicit direction.

## What outcrop captures

When you save a clip, outcrop writes:

- A markdown note containing the page **title**, **URL**, your **typed notes**, any **selected text** from the page, and a wikilink to the cropped image. Goes into the destination Obsidian vault you picked.
- The **cropped PNG image** of the region you selected. Goes into the same vault, alongside the note.
- A **per-domain history row** in outcrop's local SQLite database, recording which vault you saved to. Used to rank vaults in the popup the next time you clip from the same site.

When you enable the LLM agent (`outcrop agent enable`), the agent sees the URL, the page title, your vault display names, and any descriptions you attached to those vaults — every time the popup opens. The model itself runs in-process by default (`kronk` backend), so its inputs never leave your machine. If you choose the HTTP backend (`outcrop agent enable --backend http --endpoint URL`), the same inputs are sent to whatever endpoint you configured — see "When data leaves the machine," below.

When you opt into training-data capture (`outcrop training-data enable`), each successful clip records one extra row in the local SQLite database containing the same fields the markdown note already holds: URL, title, selected text, your notes, the candidate vault list at capture time, what the popup suggested, and what you actually picked. This dataset exists locally so that you can later fine-tune the LLM on your own routing preferences. Capture is **off by default**; the `enable` command prints exactly what gets captured before turning it on; `outcrop training-data clear` deletes every row.

## Where the data lives

Outcrop stores its own data alongside its config:

| OS | Path |
|---|---|
| macOS | `~/Library/Application Support/outcrop/` |
| Linux | `$XDG_CONFIG_HOME/outcrop/` (typically `~/.config/outcrop/`) |
| Windows | `%AppData%\outcrop\` |

Inside that directory:

- `outcrop.db` — SQLite database with vault registrations, the bearer token used by the Firefox extension, agent settings, per-domain history, and (when enabled) training-data rows.
- `models/` — GGUF model files downloaded by `outcrop agent download`. Optional; only present if you've enabled the in-process agent.

The kronk backend's llama.cpp shared libraries are downloaded under `~/.kronk/libraries/` by kronk itself the first time you enable the in-process agent.

The clipped notes and images go into the **Obsidian vault directories you registered** with `outcrop vault add`. Outcrop does not move those files anywhere else.

## What leaves the machine

By default, **nothing**. Specifically:

- The Firefox extension only talks to `http://127.0.0.1:7878` on the same machine.
- The local server only accepts requests authenticated with a token it generated locally during `outcrop init`.
- There is no analytics, no telemetry, no crash-reporter, no usage ping.
- There is no automatic update check.

There are exactly three cases where outgoing network traffic happens, and all three are at your explicit direction:

1. **`outcrop agent install-libs`** (or the same step run automatically by `outcrop agent enable`). Downloads kronk's prebuilt llama.cpp shared libraries from kronk's release host (currently a public HTTPS endpoint). One-time, ~50 MB. No identifying headers; the request looks like any other software-update fetch.
2. **`outcrop agent download`** (or the same step run automatically by `outcrop agent enable`). Downloads a GGUF model file from a public model host (currently Hugging Face). One-time per model; ~2 GB for the recommended default. Same posture: standard HTTPS, no identifying headers.
3. **The HTTP agent backend, when configured to talk to a non-loopback endpoint.** If you run `outcrop agent enable --backend http --endpoint http://your-server.example.com/v1`, every popup open will send the page URL, page title, and your vault display names to that endpoint. Outcrop prints a one-line warning at config time when you point at a non-loopback host. The default endpoint is `http://127.0.0.1:11434/v1`, which only reaches a service running on the same machine.

That's the complete list. There are no other cases where outcrop reaches out to the network.

## Permissions the Firefox extension requests

| Permission | Why |
|---|---|
| `activeTab` | Read the current tab's URL and title; capture its visible region — only when you click the toolbar button. |
| `storage` | Persist the local server URL and bearer token between sessions. |
| `scripting` | Inject the drag-rectangle overlay into the active tab on capture. |
| `notifications` | Show "Saved to <vault>" / "Save failed" after the popup closes. |
| `host_permissions: http://127.0.0.1:7878/*, http://localhost:7878/*` | Talk to the local outcrop server. |

The extension does not have access to any other site's content beyond the `activeTab`-granted scope on the tab where you click Capture.

## Updates and changes to this policy

Outcrop has no automatic update mechanism for either the server or the extension. You install updates yourself; this policy ships with the source as `PRIVACY.md`. If the policy changes in a way that affects what data leaves your machine, the change will be called out in the release notes for that version.

## Questions and reports

Open an issue at the project's source repository.

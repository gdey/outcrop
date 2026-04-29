---
rfd: 0014
title: Tray Applet (Menu Bar / System Tray)
status: ideation
created: 2026-04-28
authors:
  - gdey
tags: [design, gui, tray, fyne, systray, screen-capture]
related: [0001, 0002, 0003, 0005, 0011]
superseded-by: ~
---

# Tray Applet (Menu Bar / System Tray)

## Summary

A small native applet that lives in the macOS menu bar / Linux system tray. Exposes the day-to-day operations that today require dropping into a shell — list and edit vaults, copy the bearer token, see server status — and adds a *whole-screen* capture path so users aren't forced through the browser to clip something. Talks to the running `outcrop serve` over HTTP, the same wire contract the Firefox and Chrome extensions use.

## Motivation

Three things are friction in the current shape of outcrop:

- **CLI-only vault management.** Adding a vault, fixing a typo in a description, copying the token after a lost paste — all require shell access and remembering the subcommand. For a tool whose primary surface is "click a browser icon, drag a rectangle," the supporting operations being CLI-only is a noticeable lurch.
- **No path to clip non-browser content.** The browser extension works only on tabs. Anything outside Firefox/Chrome — a native macOS app, a Slack window, a PDF in Preview, a terminal session — can't be clipped. A tray applet that owns whole-screen capture closes that gap.
- **The server is invisible.** `outcrop serve` (or its install-service unit) runs silently. Users have no in-band feedback that it's healthy, no quick way to see *which* vaults are registered, no obvious "where do I configure this" surface. A tray icon is the most universally-understood "this app is running" signal.

A tray applet is also the natural eventual home for things that are awkward in a CLI: agent status (RFD 0006), training-data row counts (RFD 0011), per-vault recent-clip lists (RFD 0009), etc. Building it now establishes the seam those features later attach to.

## Constraints already agreed

- `outcrop serve` stays the canonical owner of the SQLite database and the on-disk markdown writes. The applet does not become a second writer to the DB or the vault directories.
- The bearer token model from `outcrop init` (RFD 0003) carries over verbatim — the applet authenticates the same way the extensions do.
- The CLI does not go away. Anything the applet does, the CLI must still be able to do (scriptability, headless servers, automation).

## Rough scope

To settle when this RFD moves to `draft`:

### 1. UI library: fyne-io/systray + fyne

Two layers of UI, two libraries — but from the same vendor so the main-thread coordination is already solved:

- **`github.com/fyne-io/systray`** for the tray icon + menu. Lightweight, cross-platform, no widget toolkit init cost on a tray-only menu. Handles "Add vault…", "Copy token", "Open settings", "Quit", and a flat list of registered vaults with a *recent-clips* submenu under each.
- **`fyne.io/fyne/v2`** for the settings window — the form-heavy "Add a vault" / "Edit vault description" / "Test server connection" UI. Used only when the user opens it from the tray menu; the heavy widget toolkit init happens lazily on first open, not at tray startup.

Why this pair specifically: fyne owns its own event loop, which on macOS wants the main thread. A standalone systray library *also* wants the main thread on macOS (NSStatusItem must be set up on it). Running an unrelated systray library alongside fyne would mean careful manual coordination of which library "owns" `NSApp`. `fyne-io/systray` is the `getlantern/systray` codebase with the main-thread dance already integrated with fyne's runtime — picking the fyne fork is essentially free since fyne is in the dependency graph anyway, and avoids a class of "tray icon doesn't appear" / "window won't focus" bugs that show up when two libraries race for the macOS main thread.

Alternative considered and rejected: **ebiten**. It's a 2D game engine — a canvas with a game loop, no native widgets, no menus, no system tray. Wrong tool for a tray applet.

Alternative considered and rejected: **`getlantern/systray` standalone, fyne added later**. Tempting because v1 might fit entirely in the tray menu (vault list, copy token, capture region — no real form needed) and could ship without fyne in the dep graph at all. But the moment the settings window needs widgets a tray menu can't express (a multi-line description editor, a file picker, a "Test connection" button with live status feedback) we'd be swapping the tray library mid-flight to avoid the main-thread fight. Better to pay the fyne dep cost upfront and pick the matched pair.

### 2. Architecture seam: dual transports, scoped by threat model

The naive answer — "the tray is just another HTTP client of `127.0.0.1:7878`" — bundles two clients with very different threat profiles behind the same transport and the same token. That's wrong. **The browser extension and the tray have intentionally different authority, and the cleanest way to encode that is intentionally different transports.**

The browser extension lives in a hostile environment we don't fully control. Other extensions, content-script edge cases, sandbox-escape bugs, and npm-supply-chain risk in the build all mean the bearer token can leak via paths we can't enumerate. The right response is to make the leaked token boring: a token that can only `POST /clip` and `GET /vaults` is much less valuable to an attacker than one that can drain the token, mutate vaults, or rewrite agent config.

The tray, by contrast, is a native process the user installed alongside outcrop, running as their UID, with no plugin model and no untrusted code in its address space. It can be trusted with privileged operations.

So the design is **dual-transport, scoped by capability**:

- **Existing HTTP listener (`127.0.0.1:7878`)** stays exactly as-is. Extension-facing. Token-authenticated. Routes: `GET /vaults`, `POST /clip`, `GET /health`. This is the only capability surface the extension ever sees, so a leaked token can never escalate beyond it.
- **New local-IPC transport** for the tray:
  - macOS / Linux: Unix domain socket at `$XDG_RUNTIME_DIR/outcrop.sock` (or `~/Library/Caches/outcrop/outcrop.sock` on macOS, since macOS doesn't ship `XDG_RUNTIME_DIR`), `chmod 0600`, owned by the user. Auth is filesystem permissions — only processes running as the user can connect.
  - Windows: named pipe (`\\.\pipe\outcrop-<uid>`) with the equivalent ACL.
  - Carries the privileged surface:

    ```
    POST   /vaults          { key, displayName, path, description? }   → 201
    PUT    /vaults/:key     { displayName?, description?, path? }      → 200
    DELETE /vaults/:key                                                 → 204
    PUT    /vaults/:key/default                                         → 200
    GET    /config/token                                                → 200  { token }
    POST   /config/token/rotate                                         → 200  { token }
    GET    /server/status                                               → 200  { runningSince, ... }
    POST   /server/tray/reload                                          → 204  (respawn the tray child)
    ```

  - Plus everything the HTTP listener exposes (the IPC surface is a strict superset). Tray uses IPC for everything; the HTTP routes are also reachable here so we don't have to maintain two client SDKs.

Why dual transport rather than scoped tokens on one HTTP listener:

1. **Privileged endpoints don't exist on the network at all.** An attacker who fully owns the extension and exfiltrates its token still can't *reach* the privileged operations — they're not bound to any TCP port. The attacker would need code execution as the user, at which point the threat model is different anyway.
2. **The auth model fits the threat model.** A network listener wants tokens. A local IPC socket wants filesystem perms. Mismatching them — putting privileged ops behind a token, or putting `clip` behind a Unix socket — wastes the strength of each.
3. **The two transports become explicit documentation.** Future readers see "this route lives on the IPC socket" and know it's not extension-reachable; "this is on `:7878`" and know it has to assume hostile.

Implementation cost is small. Go's `net.Listen("unix", path)` and `net.Dial("unix", path)` are one line each; the same `http.Handler` can serve over both transports with `http.Serve(unixListener, mux)`. Two `mux`es with different routes registered, a thin permission check on the IPC mux, done.

### 2a. CLI: stays direct-DB for v1, may grow IPC entry points later

A natural follow-up question is whether the CLI should also migrate to IPC. **For v1 of this RFD: no.** The CLI keeps direct-DB access exactly as it has today.

The "single writer to the DB" argument that would push the CLI to IPC is weaker than it sounds. RFD 0012 already moved the server to fingerprint-on-every-request: the server re-checks `meta.fingerprint` before every request handler runs and rebuilds in-memory state if it changed. So when the CLI writes to the DB underneath the running server, the server picks it up on the next request — no stale-cache problem to solve. SQLite WAL handles concurrent writes independently. The "two writers" cost is *aesthetic* (the diagram has two arrows pointing at the DB), not behavioral.

What the CLI gives up by going IPC-only is concrete and load-bearing:

- **Bootstrap.** `outcrop init` creates the DB. There is no server yet, no socket to talk to. This will *always* be direct-DB. Hard constraint.
- **Recovery / debugging.** Inspecting or fixing the config when the server is broken or refusing to start. "Open the DB and fix it" must keep working.
- **Scripted automation.** A setup script doing `outcrop vault add … && outcrop vault add … && outcrop vault default …` should not require waiting for a server to start. Direct-DB lets it just work.
- **The "doesn't need server running" property** for `outcrop config show`, `vault list`, etc. Useful for debugging, useful in install scripts.

The clean rule: **anything that's part of "the server is broken and I need to fix it, or the server isn't running yet" must work without the server.** That's a stable set — init, config inspect, vault list/edit, token rotate. For those, direct-DB is the right tool *forever*, not just in v1.

What IPC *does* unlock is **new** CLI capabilities that depend on the server being live, where IPC gives them something direct-DB can't:

- **`outcrop status`** — connect to the IPC socket; success = server up, ECONNREFUSED = server down. This is more reliable than probing `127.0.0.1:7878`, because the port is configurable and the CLI would have to read config to know what to probe; the socket path is derivable from the same XDG/macOS-Library logic as the DB path, no config-read needed.
- **`outcrop agent test <url>`** over IPC reuses the **already-loaded model** in the running server's memory instead of paying the ~2s libllama init + GGUF mmap on every invocation. Material UX win.
- **Live agent / training-data status** — anything where "what the server believes right now" is more meaningful than "what the DB says, computed offline."

The pattern: **IPC opens new capabilities, it doesn't replace existing ones.** Existing direct-DB CLI commands keep their server-not-required property. New commands that genuinely need live server state are new entry points and are honest about the dependency. If a future RFD wants to migrate any *specific* existing command to IPC (e.g. because reusing the loaded model would dominate its runtime), it can — case by case, not a forced sweep.

### 2b. Alternatives considered and rejected

- **Single HTTP listener, scoped tokens.** Issue an "extension token" with `clip,vaults:read` scopes and a "tray token" with `*` scopes; gate routes on scope claims. Solves the *authorization* layer but not the *attack-surface* layer — the privileged endpoints still exist on the TCP port the extension uses, so a future bug in the scope-check middleware is a privilege escalation. Dual transport eliminates the route from the extension's reachable surface entirely.
- **Tray shells out to `outcrop` CLI subcommands.** Zero new server code. But: brittle (parsing CLI output is fragile, error reporting is via exit codes and stderr text, no streaming progress for things like vault-add when the path doesn't exist yet), and forecloses on a future where the tray might run on a *different machine* than the server (over a tunnel or LAN). IPC is the right shape long-term and has the same on-machine simplicity.
- **Tray opens the SQLite DB directly.** Fastest to implement (zero new endpoints). But: now the tray has to know things the server already knows (config-dir resolution, migration state, atomic-write semantics for vault paths), and the privileged-operations surface lives in the tray binary instead of in one place on the server. Centralizing in the server matches where everything else lives.

### 3. Process model: separate binary, parent–child via `outcrop serve --tray`

The applet is its own subcommand: `outcrop tray`. The same binary, a different `os.Args[1]`. `install-service` still installs only `outcrop serve` — the launchd/systemd unit doesn't need to know about the tray.

`outcrop serve` gets a new flag:

```
outcrop serve --tray=auto      # default — spawn tray child if GUI env detected
outcrop serve --tray=on        # always spawn (error if no GUI)
outcrop serve --tray=off       # never spawn
```

GUI detection:

- macOS: assume yes (no headless macOS server is going to run `outcrop serve --tray=auto` and care).
- Linux: `DISPLAY != "" || WAYLAND_DISPLAY != ""`. Headless servers (no display) get nothing.

When `serve` decides to spawn, it `exec.Command(<self>, "tray")`s a child (where `<self>` is `os.Executable()` — re-using the running binary by absolute path so a tray rebuild doesn't drift from the spawning serve). It wires the child's stdout/stderr to its own log stream prefixed `[tray]`, and reaps on exit. If `serve` exits, it sends SIGTERM to the tray child.

#### Build-tag gating: tray code is opt-in at compile time

The tray pulls in **fyne-io/systray + fyne**, which both require **CGO** on every platform that matters (macOS Cocoa, Linux GTK + AppIndicator, Windows Win32). The server itself is intentionally CGO-free — kronk uses purego at runtime so cross-compile works without a C toolchain on the build host.

To keep both properties — server stays CGO-free, tray gets the native UI it needs — the tray code is gated behind a Go build tag:

```
go build ./cmd/outcrop                  # default: server only, CGO-free,
                                        # `outcrop tray` errors out
go build -tags tray ./cmd/outcrop       # CGO-on, tray UI included
```

Files using fyne / systray sit under `//go:build tray`. The `outcrop tray` subcommand has a `//go:build !tray` stub that exits with code 65 and a clear "this binary was built without tray support; rebuild with `-tags tray`" message. The parent `serve --tray=auto` handler treats exit-65 as "tray not compiled in" and stops trying to spawn — the user ran a server-only build and asked for tray; we shouldn't busy-loop.

#### Release-pipeline implications: per-platform CI matrix, Windows dropped

Because tray-enabled builds are CGO-on, the previous "single ubuntu runner cross-compiles every target" goreleaser config no longer works. The new shape:

- **Per-platform CI matrix.** `release.yml` runs goreleaser natively on `macos-latest` (for darwin/amd64 + darwin/arm64) and `ubuntu-latest` (for linux/amd64; linux/arm64 via the `ubuntu-24.04-arm` runner). Each runner builds the tray-enabled binary for its native OS, and a final job assembles all artifacts onto one GitHub release.
- **Windows is dropped from the release** for now. The browser extension covers most Windows clipping use cases; adding the Windows tray means a third runner and Windows-specific CGO header juggling, which we'd rather defer until somebody actually asks for it.

Goreleaser still drives each runner — checksums, archive naming, changelog all stay the same — but the build matrix is the workflow's job, not goreleaser's.

#### Restart policy: exit-code-aware, no flag knob

The behavior when the tray child exits is decided by **exit code semantics**, not by another `--tray` value:

- **Exit code 0** (the user clicked "Quit Outcrop tray" from the menu) → server respects it and does *not* respawn. The Quit menu item actually does what it says.
- **Exit code 65** (the tray subcommand's "not compiled in" stub) → server logs once and stops trying — this binary will never have a tray, no point retrying.
- **Any other non-zero exit** (crash, panic, signal-killed, GPU driver tantrum, etc.) → server respawns the child with bounded backoff: up to 3 retries within 60s, exponential delay (1s, 2s, 4s). After 3 failed retries, server logs loudly (`[tray] giving up after 3 crashes in 60s; run \`outcrop tray reload\` to retry`) and stops trying. The server itself keeps running — the tray is decorative, not load-bearing.

#### Restart policy: exit-code-aware, no flag knob

The behavior when the tray child exits is decided by **exit code semantics**, not by another `--tray` value:

- **Exit code 0** (the user clicked "Quit Outcrop tray" from the menu) → server respects it and does *not* respawn. The Quit menu item actually does what it says.
- **Non-zero exit** (crash, panic, signal-killed, GPU driver tantrum, etc.) → server respawns the child with bounded backoff: up to 3 retries within 60s, exponential delay (1s, 2s, 4s). After 3 failed retries, server logs loudly (`[tray] giving up after 3 crashes in 60s; run \`outcrop tray reload\` to retry`) and stops trying. The server itself keeps running — the tray is decorative, not load-bearing.

This makes "auto-restart" mean *resilience against bugs*, not *overriding user intent*. No new flag value is needed; `--tray=auto/on/off` keeps clean spawn-decision semantics.

To bring the tray back after the user quit it (or after backoff gave up), there's a runtime command:

```
outcrop tray reload    # IPC: POST /server/tray/reload — server respawns the tray child
```

This adds one route to the IPC mux from §2 and is also the natural surface for "I just rebuilt the tray binary, kick the parent to pick it up" during development.

Alternative considered and rejected: **`--tray=once`** (or any flag value that says "spawn but never auto-restart"). The exit-code rule already gives users a clean way to stop the tray (click Quit), so a flag mode for "spawn and stay one-shot" doesn't earn its keep. If you genuinely want a one-shot for testing, `--tray=off` on serve plus running `outcrop tray` manually as a separate process gives you exactly that without any new flag value. Conversely, an "always respawn even on user-quit" mode would defeat the Quit menu item — there's no clean way to dismiss the tray short of stopping the server, which is hostile UX. Both modes are rejected.

Alternative considered: **single-process, server + tray in one binary**. Rejected because:
- systray's main-thread requirement on macOS conflicts with HTTP server logging/lifecycle.
- A crashing fyne window (out-of-memory, GPU driver bug, etc.) would take the server down with it.
- Crash isolation matters more than the small extra cost of one process.

### 4. Whole-screen capture

The tray menu has a "Capture region…" item that does for the desktop what the browser extension does for a tab.

The hard part is *interactive region selection* — drawing the selection rectangle, the dim mask, the size readout — across macOS / X11 / Wayland. Two paths:

- **Shell out to native tools (recommended for v1).** Mature, no GUI code to write:
  - macOS: `screencapture -i -t png -` (built-in since 10.x; outputs PNG to stdout).
  - Linux/Wayland: `slurp | grim -g - -t png -` (de facto pair on most Wayland desktops; user installs them via package manager).
  - Linux/X11: `import -window root` (ImageMagick) or `maim` + `slop` (modern combo). User picks; tray probes for what's installed.
  - Windows: `Snipping Tool` integration is awkward; v1 may simply not ship Windows screen capture (browser extension covers most use cases there) or pin to a specific tool like `nircmd`.
- **In-process pure Go.** `kbinani/screenshot` for full-screen image grab; build the region overlay ourselves with fyne. Cross-platform, no shelling out. But: implementing the overlay (multi-display, crosshair cursor, escape-to-cancel, keyboard fallback) is real work — easily a week of polish — and we'd be re-doing what `screencapture -i` does well.

Recommendation: shell out for v1, leave the door open for replacing per-OS implementations with in-process paths if friction emerges.

Once the region PNG exists, the applet POSTs to `/clip` with the same shape the browser extension uses. Vault selection happens in a tiny modal (fyne window) that opens after the region is captured: vault picker + notes textarea + Save. Same flow as the browser extension's preview card, just outside the browser.

### 4a. What goes in `url` and `title` for a desktop capture

The browser extension sends `url` (the page URL) and `title` (the document title) when it calls `GET /vaults?url=&title=` to get a personalized vault ranking, and again when it `POST /clip`s. Per-domain history (RFD 0003) and the LLM router (RFD 0005) both consume those fields. A desktop capture from the tray has no page URL — but it has equivalents that *should* feed the same ranking machinery so users get the same "outcrop suggested the right vault" feel.

Schema and conventions:

- **`url` = `app://<bundle-id-or-equivalent>`** — a synthetic URL identifying the active app. Stable, machine-readable, slots into the same per-domain history table as a real URL because `app://com.apple.Preview` has the same shape as `https://example.com`.
  - macOS: bundle identifier (`com.apple.Preview`, `com.tinyspeck.slackmacgap`).
  - Linux: `.desktop` file basename or X11 `WM_CLASS` (`firefox`, `code`, `org.gnome.Terminal`).
  - Windows: process executable basename without extension (`Slack`, `Code`, `WINWORD`).
- **`url` = `app://desktop`** — fallback when no foreground app can be determined, or when the user is capturing a region that spans the desktop / wallpaper.
- **`title` = the localized app name** — `"Slack"`, `"Préview"`, etc. Human-readable, what the LLM router can chew on, and what the user sees in the post-capture vault picker so they can recognize what they captured.

What the tray does *not* attempt for v1:

- **Window title.** On macOS, reading another app's window title via the Accessibility API requires a separate permission grant — a second scary OS dialog stacked on top of the Screen Recording prompt. v1 ships without it; the title is just the app name. v2 / opt-in: tray asks for Accessibility, and if granted, replaces the localized app name with `"<app name> — <window title>"` for richer LLM signal. If the user denies, fall back silently.
- **Document path / sub-resource.** `app://com.apple.Preview/manual.pdf` would be lovely (the LLM could route differently for PDFs vs. images); but extracting it requires app-specific introspection per platform. Out of scope for v1.
- **Wayland window title.** Wayland's security model deliberately hides other clients' titles from arbitrary apps. Most compositors expose this only through compositor-specific D-Bus or extensions. v1 punts: Wayland tray sends just the app identifier (where available via the `.desktop` activation hint or `xdg-desktop-portal`), and `app://desktop` otherwise.

Detection paths per platform:

- **macOS**: `NSWorkspace.shared.frontmostApplication.bundleIdentifier` + `.localizedName`. No permission required. Cgo or a small Swift helper invoked via `os/exec`.
- **Linux/X11**: `xdotool getactivewindow getwindowname` and `xprop -id … WM_CLASS`. Tray probes for `xdotool`/`xprop` at startup; if neither is installed, falls back to `app://desktop`.
- **Linux/Wayland**: best-effort via `xdg-desktop-portal`'s ActiveApplication interface where available; otherwise `app://desktop`.
- **Windows**: `GetForegroundWindow` + `GetWindowText` + `GetModuleFileNameEx` via `golang.org/x/sys/windows`. No permission prompt.

Why this matters: vault descriptions written for a casual user might say `"Tech — programming, software, terminal sessions, code editors"`. With `url=app://com.microsoft.VSCode` and `title="Code"`, the LLM router has enough to rank that vault first. Per-domain history accumulates the same way — once you've sent two clips from VS Code to Tech, the history table promotes Tech to the top of the ranking for any future `app://com.microsoft.VSCode` capture, exactly as it does for `https://news.ycombinator.com`.

### 5. macOS Screen Recording permission

Capturing the screen on macOS requires the user to grant **Screen Recording** permission via System Settings → Privacy & Security. Two practical issues:

- **Plain CLI binaries can't show a clean permission dialog.** macOS's permission UI references the *requesting process*; for a command-line `outcrop` binary on `$PATH`, the dialog says something opaque like "Terminal is requesting…" or attributes the request wrongly. The fix is to bundle outcrop as a `.app` (or at least the tray's screenshot subcommand). Goreleaser supports `.app` bundling on darwin; this is doable.
- **First-run flow.** Triggering `screencapture` for the first time pops the dialog, the user clicks Allow, *and is required to restart the requesting app* (in our case, the tray) before permission takes effect. The tray menu should detect the "permission denied" failure mode, show a small notification ("Grant Screen Recording in System Settings, then quit and reopen the tray"), and not retry indefinitely.

Pragmatic v1: let the OS handle the dialog the first time, document the restart requirement in the install guide, defer `.app` bundling unless it materially improves the UX.

### 6. Tray menu shape (rough)

```
┌────────────────────────────────────────────┐
│ ● Outcrop                       (0.1.x)    │
├────────────────────────────────────────────┤
│ Capture region…                  ⌘⇧4       │  ← screen capture entry
├────────────────────────────────────────────┤
│ Vaults                                     │
│   Personal              (12)               │  ← default; position above the divider says so
│ ─────                                      │
│   Recipes               (0)                │
│ ✓ Tech                  (3)                │  ← ✓ marks the last-used vault
│   Add vault…                               │
├────────────────────────────────────────────┤
│ Copy bearer token                          │  ← copies to clipboard, no display
│ Server status: ● running                   │
│ Open settings…                             │  ← opens fyne window
├────────────────────────────────────────────┤
│ Quit Outcrop tray                          │  ← does NOT stop `outcrop serve`
└────────────────────────────────────────────┘
```

Visual encoding rules:

- **Position above the divider = the default vault.** Whatever `outcrop vault default <key>` last set, sits at the top, alone, separated from the rest by a horizontal rule. No `(default)` parenthetical — the position already says it.
- **`✓` checkmark = the last-used vault.** Defined as the vault the most-recent successful clip landed in, regardless of which client (extension, tray, CLI script) initiated it. Comes from the per-domain history table — `MAX(created_at)` across all rows. Survives tray restarts.
- **`(N)` after the name = the clip count for that vault.** Total across all time, not "this week" — simpler, no time-window decision to argue about. If the count is 0, render it anyway so all rows have the same shape.
- **Sort order below the default**: alphabetical by display name. Predictable and browsable.
  - **If the vault list grows past a comfortable menu length** (likely thresholds: ~10 entries on macOS, ~15 on Linux/Windows): switch to `ORDER BY clip_count DESC, display_name ASC` so the most-active vaults sit near the top of the body and rarely-used ones drop. Still alphabetical *within* a count bucket.
- **Edge case — empty DB / no clips yet**: no `✓` on any vault, all counts show `(0)`. The default still sits above the divider.
- **Edge case — default *is* the last-used vault**: `✓` renders next to the default's row at the top, body stays alphabetical. Both signals legible at once.

The "Quit Outcrop tray" item explicitly does not stop the server — that's what `outcrop install-service uninstall` is for. The applet is for *managing* the server, not its lifecycle.

This menu is a **management surface**, not a target picker for the next capture. The domain-aware ranking (per-domain history + LLM nudge) that the browser popup uses doesn't apply here, because the URL/app isn't known until *after* the capture happens. The post-capture vault picker (a small fyne window that opens once the region PNG exists) is where that ranking shows up — same role as the browser popup, just outside the browser.

### 7. Settings window (fyne)

Opens from "Open settings…". Tabbed:

- **Vaults**: list with edit/rename/delete/set-default; Add button.
- **Server**: read-only display of resolved config path, port, listen address; "Copy token" button; restart hint with platform-specific command (`launchctl …` / `systemctl --user …`).
- **Agent** (post RFD 0006): status, model, capabilities; enable/disable.
- **Training data** (post RFD 0011): row count, suggestion-vs-override stats, Disable / Clear buttons.

v1 ships only **Vaults** and **Server**. The other tabs hook in once their respective RFDs land.

### 8. Where does it run *from*?

- macOS: launchd already starts `outcrop serve` at login (RFD'd in install-service docs). With `--tray=auto`, the tray child appears in the menu bar shortly after login.
- Linux: systemd user unit. Same posture — `--tray=auto` spawns the tray when the unit starts.
- Windows: the Task Scheduler entry runs `outcrop serve`; `--tray=auto` spawns the tray child. Windows tray icon shows in the notification area.

`install-service` does not need to change — the tray follows the server transparently when the flag is set.

## Out of scope

- **Stopping/starting the server from the tray.** Service lifecycle stays in the OS-native units (`launchctl`, `systemctl`, Task Scheduler). The tray displays running/not-running status; it doesn't manage it. (Re-evaluate if users actually want this; for v1 it's a needless surface to maintain.)
- **Mobile.** No menu bar, no tray; iOS/Android have no analog. Out of scope permanently for this RFD.
- **Cross-machine tray.** The applet talks to a *local* server. Driving a remote outcrop from a tray on a different machine is interesting but is a different design (auth, tunneling, mDNS discovery) and gets its own RFD if it ever happens.
- **Click-element capture (RFD 0002) port to the tray.** Whole-screen capture is the v1 scope; clicking a single window/element to capture it is structurally similar but is its own UX — kept under RFD 0002.

## Status notes

- 2026-04-28 — Created as `ideation`. Driven by friction with CLI-only vault management, the gap that browser extensions can't clip non-browser content, and the value of a visible "outcrop is running" signal.
- 2026-04-28 — Closed the systray-library open decision: committed to `fyne-io/systray` paired with `fyne.io/fyne/v2`, since fyne is already a hard dep for the settings window and the matched pair avoids macOS main-thread races.
- 2026-04-28 — Reframed the architecture seam (§2). Originally proposed "tray is just another HTTP client of `:7878`"; revised to **dual transports scoped by threat model**: the existing HTTP listener stays narrow (extension-only, leaked-token-is-boring), and a new local-IPC transport (Unix socket / named pipe) carries the privileged surface for the tray. The CLI explicitly stays direct-DB for v1; IPC for the CLI is allowed but only for *new* commands where it genuinely unlocks something (model reuse, liveness probe).
- 2026-04-28 — Added §4a defining the `url` / `title` shape for desktop captures: synthetic `app://<bundle-id>` URLs (or `app://desktop` fallback) so per-domain history and the LLM router both work on tray captures the same way they work on browser captures. v1 uses the localized app name as the title and skips window-title-via-Accessibility; v2/opt-in extends to window titles where permission is granted.
- 2026-04-28 — Tightened §6 menu visual encoding: position-above-divider = default, `✓` = last-used (most-recent successful clip across all clients), `(N)` = total clip count, alphabetical body sort with a count-then-alpha fallback once the list grows past ~10–15 entries.
- 2026-04-28 — Settled tray restart policy in §3: exit-code-aware (clean exit = honor user's Quit; non-zero = respawn with bounded backoff), no new `--tray` flag value. Added `outcrop tray reload` as a runtime IPC command to bring the tray back after a user-quit or after backoff gave up. Explicitly rejected `--tray=once` and "always-respawn-even-on-quit" modes.
- 2026-04-28 — Reframed §3 to honestly capture the CGO/release-pipeline implications of fyne-io/systray. The earlier "same binary, different subcommand keeps the goreleaser story unchanged" claim was wrong — fyne+systray require CGO on every platform that matters, which conflicts with the pure-Go cross-compile config. Resolution: tray code is gated behind a `//go:build tray` build tag (default `outcrop` stays CGO-free; `-tags tray` builds get the tray UI). The release pipeline moves to a per-platform CI matrix (macos-latest + ubuntu-latest, with `ubuntu-24.04-arm` for linux/arm64). Windows is dropped from the release for now — the extension covers most Windows clipping use cases, and adding tray on Windows means a third runner + Windows CGO juggling that's better deferred. Move to `draft` after feedback on (a) whether `outcrop serve --tray=auto` is the right default, and (b) whether shelling out to native screen-capture tools is acceptable for v1 versus building the region selector in-process.

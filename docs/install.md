# Installing Outcrop

Outcrop has two parts: a **local Go server** (this guide) and a **Firefox extension** (see the project's GitHub Releases for the signed `.xpi`). Both run on your machine; nothing leaves it (see [PRIVACY.md](../PRIVACY.md) for the full data-handling story).

This document covers installing the **server** on macOS, Linux, and Windows, plus the optional run-on-login setup and LLM agent setup.

---

## 1. Download

Pre-built binaries live on the [GitHub Releases](https://github.com/gdey/outcrop/releases) page. Pick the archive matching your OS and architecture:

| OS | Architecture | Archive |
|---|---|---|
| macOS (Apple Silicon) | `arm64` | `outcrop_<version>_darwin_arm64.tar.gz` |
| macOS (Intel) | `amd64` | `outcrop_<version>_darwin_amd64.tar.gz` |
| Linux | `arm64` | `outcrop_<version>_linux_arm64.tar.gz` |
| Linux | `amd64` | `outcrop_<version>_linux_amd64.tar.gz` |
| Windows | `amd64` | `outcrop_<version>_windows_amd64.zip` |

Each archive contains the `outcrop` binary, `LICENSE`, `PRIVACY.md`, `README.md`, this `install.md`, and the relevant RFDs.

### Verify the download

Each release also publishes a `checksums.txt` listing SHA-256 digests for every archive.

```sh
# macOS / Linux
sha256sum -c checksums.txt 2>&1 | grep OK
```

```powershell
# Windows PowerShell
Get-FileHash outcrop_*_windows_amd64.zip -Algorithm SHA256
# Compare manually against checksums.txt
```

### Place the binary

After extracting, move the binary somewhere on your `PATH` so the rest of this guide's commands work:

| OS | Suggested path |
|---|---|
| macOS, Linux | `/usr/local/bin/outcrop` (system-wide) or `~/.local/bin/outcrop` (user) |
| Windows | `%LOCALAPPDATA%\Programs\outcrop\outcrop.exe`, then add that folder to `PATH` |

```sh
# macOS / Linux
mkdir -p ~/.local/bin
mv outcrop ~/.local/bin/
chmod +x ~/.local/bin/outcrop
```

Confirm:

```sh
outcrop help
```

---

## 2. First-run setup

```sh
outcrop init
```

This creates outcrop's local SQLite database and prints a **bearer token**. Copy it — you'll paste it into the Firefox extension on first run.

The database lives at:

| OS | Path |
|---|---|
| macOS | `~/Library/Application Support/outcrop/outcrop.db` |
| Linux | `$XDG_CONFIG_HOME/outcrop/outcrop.db` (typically `~/.config/outcrop/outcrop.db`) |
| Windows | `%AppData%\outcrop\outcrop.db` |

`outcrop config path` prints the resolved path on your machine.

### Register a destination folder

Outcrop writes plain `.md` + `.png` files into the folder you point it at. An Obsidian vault is one example; any markdown-notes folder works (Foam workspace, Logseq graph, plain folder of notes, git-backed notes repo, etc.).

```sh
outcrop vault add \
    --description "life admin, journaling, news, things to remember" \
    Personal /path/to/your/notes-folder
```

The `--description` is optional but **strongly recommended** if you plan to use the LLM agent — vaults without a description are at a routing disadvantage. Add as many folders as you have:

```sh
outcrop vault add --description "ML papers, programming, software engineering" Tech /path/to/Tech-Vault
outcrop vault add --description "cooking and recipes" Recipes /path/to/Recipes-Vault
```

`outcrop vault list` shows what's registered.

### Try a manual run

```sh
outcrop serve
```

This binds to `127.0.0.1:7878` and waits for clip requests. Leave it running; in another terminal:

```sh
outcrop config show           # prints config (token redacted)
outcrop config show --show-token   # prints with the token revealed
```

`Ctrl+C` to stop. Next section sets it up to start automatically.

---

## 3. Run at login

The `outcrop install-service` subcommand drops a launch unit appropriate for your OS, enables it, and starts the server.

```sh
outcrop install-service
```

This is **idempotent** — running it again replaces the existing unit. To check status:

```sh
outcrop install-service status
```

To remove:

```sh
outcrop install-service uninstall
```

What it does, per OS:

### macOS

Writes `~/Library/LaunchAgents/me.gdey.outcrop.plist` (a launchd plist) and registers it via `launchctl bootstrap gui/<your-uid>`.

- Logs go to `~/Library/Logs/outcrop/outcrop.{out,err}.log`.
- The service auto-restarts on crash (`KeepAlive: true`).

If you'd rather do it by hand, the plist is human-readable; see `Library/LaunchAgents/me.gdey.outcrop.plist` after running install-service to learn the shape.

### Linux

Writes `~/.config/systemd/user/outcrop.service` (a systemd user unit) and runs `systemctl --user daemon-reload && systemctl --user enable --now outcrop.service`.

- View logs with `journalctl --user -u outcrop -f`.
- Auto-restarts on failure (`Restart=on-failure`).
- If your distro doesn't run a systemd user instance (raw containers, some minimal setups), the unit is still written but won't start; `outcrop serve` from the shell or a wrapper script is the workaround.

You may also want to run `loginctl enable-linger $USER` so the service keeps running when you're not logged in (most desktop distros default this on; servers don't).

### Windows

Creates a Task Scheduler entry named `Outcrop` that runs `outcrop serve` at every user logon.

- View / edit it in **Task Scheduler** → **Task Scheduler Library** → **Outcrop**.
- Trigger is `ONLOGON` (current user).
- The Windows trigger doesn't include automatic crash-restart; if you need that, edit the task in Task Scheduler and add a "Restart on failure" condition.

---

## 4. Install the Firefox extension

Pre-built `.xpi` files are attached to GitHub Releases. Download the `.xpi`, then in Firefox:

1. Open `about:addons`.
2. Click the gear icon → **Install Add-on From File…**
3. Pick the downloaded `outcrop-<version>.xpi`.
4. The options page opens automatically. Paste the token from `outcrop init`, click **Test connection**.

If the test reports the server is unreachable, confirm `outcrop serve` (or the run-at-login service) is running.

Capture flow: click the toolbar icon → pick a vault → **Capture** → drag a rectangle → type notes → **⌘/Ctrl+Enter** to save (or **Escape** to cancel).

---

## 5. Optional: enable the LLM agent

By default, the popup ranks vaults by your most-recently-used vault per registrable domain — fine for sites you visit often, weaker for fresh URLs. Enabling the agent uses a small local LLM to pick the right vault from the URL, title, and your vault descriptions.

```sh
outcrop agent enable
```

This is interactive; it walks you through:

1. Installing kronk's prebuilt llama.cpp shared libraries (~50 MB, one-time, downloaded to `~/.kronk/libraries/`).
2. Downloading the recommended default GGUF model (~1.9 GB, one-time, stored next to outcrop's database).
3. Wiring everything into outcrop's config.

Once done, the running server picks up the change on the next popup open — no restart needed.

To verify:

```sh
outcrop agent status
outcrop agent test https://example.com/some-article "Some Title"
```

If you'd rather use ollama or another OpenAI-compatible local LLM:

```sh
outcrop agent enable --backend http --model llama3.2:3b --endpoint http://127.0.0.1:11434/v1
```

To turn it off:

```sh
outcrop agent disable
```

---

## 6. Optional: capture training data for fine-tuning

Every successful clip can record one labelled `(input → chosen vault)` row, so you can later fine-tune the model on **your** routing preferences. Off by default; the enable command shows exactly what gets captured before turning on.

```sh
outcrop training-data enable      # opt in
outcrop training-data status      # rows + suggestion-vs-override stats
outcrop training-data disable     # opt out (existing rows preserved)
outcrop training-data clear       # wipe the table
```

The dataset stays local. There's no built-in export tooling yet — query directly with `sqlite3 $(outcrop config path) "SELECT * FROM training_examples …"` if you need to inspect it.

See [RFD 0011](rfd/0011-training-data-capture.md) for the design.

---

## Troubleshooting

### "init: unable to load library: libggml.dylib not found"

The kronk in-process backend needs llama.cpp's shared libraries on disk. Run:

```sh
outcrop agent install-libs
```

This is normally automatic during `outcrop agent enable`; running it explicitly is the recovery path if the auto-install was skipped or interrupted.

### "Address already in use" on `outcrop serve`

Another `outcrop serve` is already running, or another process has port 7878. Find it:

```sh
# macOS / Linux
lsof -iTCP:7878 -sTCP:LISTEN

# Windows
netstat -ano | findstr :7878
```

If it's an `outcrop` you don't remember starting (e.g., the run-on-login service), that's expected — the second instance refuses to bind. You don't need both.

### Extension says "Server is unreachable"

1. Confirm the server is running: `outcrop install-service status` or check the process list.
2. Confirm the URL in the options page matches the listen address (default `http://127.0.0.1:7878`).
3. Confirm the token in the options page matches `outcrop config show --show-token`.

### Service starts but immediately exits

Check the platform-specific logs:

| OS | Where |
|---|---|
| macOS | `~/Library/Logs/outcrop/outcrop.err.log` |
| Linux | `journalctl --user -u outcrop --since today` |
| Windows | Open Task Scheduler → **Outcrop** → **History** tab |

The most common cause is that `outcrop init` was never run on this machine (no token in the DB).

### Removing outcrop entirely

```sh
outcrop install-service uninstall   # remove run-on-login
rm -rf ~/Library/Application\ Support/outcrop   # macOS data
rm -rf ~/.config/outcrop                          # Linux data
rm -rf ~/.kronk                                   # kronk's libllama and any agent-downloaded models that landed under ~/.kronk
```

(Use the appropriate path for your OS; `outcrop config path` showed it during setup.)

If you also want to clean up the binary, remove `outcrop` from wherever you placed it in step 1.

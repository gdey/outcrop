---
rfd: 0012
title: Live Config Reload
status: committed
created: 2026-04-27
authors:
  - gdey
tags: [design, server, runtime, hot-reload]
related: [0001, 0003, 0005, 0011, 0013]
superseded-by: ~
---

# Live Config Reload

## Summary

`outcrop serve` picks up `meta`-table config changes — agent enable/disable, backend swap, model path, training-data toggle — without a server restart. A fingerprint of the relevant `meta` keys, computed on every `GET /vaults` call, drives a single-entry `Scorer` cache: identical fingerprint → return the cached Scorer; different fingerprint → build the new one, schedule the old one's `Close` after a short grace period, swap.

No new HTTP endpoints, no signals, no pidfiles, no IPC plumbing. The CLI just writes `meta` keys as it does today; the server discovers the change on the next request.

## Motivation

Before this RFD, every config-changing CLI command — `outcrop agent enable`, `outcrop agent disable`, `outcrop training-data enable`, etc. — printed a hint like "Restart `outcrop serve` to pick up the new config." For a single-user tool that's friction the user pays repeatedly while iterating: tweak descriptions, re-enable agent with a different model, toggle training-data capture. Every iteration, restart.

The agent (RFD 0005) makes this worse, not better. Re-enabling with a different `--model` is a perfectly reasonable iteration step; needing to kill and re-launch the daemon to pick that up is annoying.

## Constraints already agreed

- **RFD 0001 §"Routing policy lives on the server"** — the server is authoritative for config; CLI tools are thin writers to it.
- **RFD 0003 §"Storage"** — config and history live in a single SQLite DB. Bearer-token auth on every HTTP route.
- **RFD 0005 §"Configuration"** — agent config is stored as `meta` keys. The seven keys (`agent_enabled`, `agent_backend`, `agent_model_path`, `agent_endpoint`, `agent_model`, `agent_api_key`, `agent_timeout_ms`) drive the Scorer.
- **RFD 0011** — `training_data_enabled` is also a `meta` key; capture is read on each `POST /clip` (no caching needed there).

## Alternatives considered

Six options, in roughly increasing simplicity:

### 1. HTTP control endpoint (`POST /admin/reload`)

CLI commands write `meta`, then `POST /admin/reload` to the server. Authenticated with the same bearer token.

- **Pro:** explicit; trivial to test (curl + a token).
- **Con:** new HTTP surface; CLI has to learn how to find the server (listen address from `meta`); failure modes ("server not running") need handling that today's CLI doesn't have.

### 2. SIGHUP + pidfile

CLI looks up the server's PID via a pidfile in the config dir, sends `SIGHUP`. Server's signal handler triggers reload.

- **Pro:** classic, well-understood Unix idiom.
- **Con:** pidfiles are fiddly (stale entries when the server crashed, races between `outcrop serve` instances). Windows compatibility is awkward (no real `SIGHUP`). Adds a lifecycle concern (write pidfile on start, remove on clean exit, deal with unclean exits).

### 3. Unix domain socket / Windows named pipe

The server listens on a control socket alongside the HTTP port. CLI connects, sends a "reload" command.

- **Pro:** low-overhead, scoped to localhost.
- **Con:** OS-specific (UDS vs. named pipe); two listeners to manage; another auth surface; no clear win over (1).

### 4. SQLite `sqlite3_update_hook`

modernc.org/sqlite exposes the libsqlite3 update_hook callback. The server registers a hook that fires on `meta` writes.

- **Pro:** no polling, no IPC.
- **Con:** **the hook only fires for writes made through *its own* connection.** The CLI process opens a separate connection; its writes don't trigger the server's hook. Useless for cross-process notification.

### 5. `PRAGMA data_version` polling + fingerprint cache

`PRAGMA data_version` is a per-database integer that increments whenever any process commits a write to the file (including writes from a separate process). The server reads it on each `GET /vaults`; if it matches the cached value, return the cached Scorer. If it changed, re-read `meta` keys and rebuild the Scorer when the agent fingerprint also changed.

- **Pro:** cross-process; cheap (microseconds per `PRAGMA` query); no IPC.
- **Con:** in *our* workload `/vaults` is almost always preceded by a write — a typical clip workflow is `GET /vaults` → user picks → `POST /clip` (which runs `RecordClip` → bumps `data_version`; possibly `RecordTrainingExample` → bumps it again). The next `/vaults` therefore sees `data_version` changed, falls into the slow path (re-read `meta`, recompute fingerprint, find it unchanged, update cached `dv`, return cached Scorer), and pays the same ~350 µs that fingerprint-on-every-call pays — *plus* the `PRAGMA` query. Net cost: equivalent to (or marginally worse than) option 6 in the current architecture, with extra moving parts.

  This option becomes meaningfully faster than option 6 **only after RFD 0013's DB split**: with config and analytics in separate database files, `RecordClip` / `RecordTrainingExample` write to `analytics.db` and don't perturb `config.db`'s `data_version`. The fast path then fires for nearly every `/vaults` call, with the slow path reserved for actual config writes (rare). At that point this option strictly dominates option 6.

### 6. Fingerprint on every `/vaults` (CHOSEN)

Compute a fingerprint of the seven agent `meta` keys on every `GET /vaults`. If it matches the cached value, return the cached Scorer. If not, build a new Scorer, swap, schedule the old one's `Close` after a grace period.

- **Pro:** no `PRAGMA`, no polling primitive at all. Trivial to reason about: "fingerprint says identical → use cache; says different → rebuild." Equivalent in real-world cost to option 5 *in the current single-DB architecture*, and simpler.
- **Con:** every `/vaults` reads seven `meta` keys (~350 µs). In absolute terms this is invisible — `/vaults` already does an `agent.Scorer.Score` call dominated by either history lookup (~1 ms) or LLM inference (~200 ms – 1+ s). The 350 µs of meta-read overhead is below the noise floor.
- **Future:** if RFD 0013's DB split ships, switch to option 5. The fingerprint logic stays as defence-in-depth; we just add a `PRAGMA data_version` fast path on top. The migration is small and the win becomes real.

## Decision

Option 6 — fingerprint on every `/vaults`. Equivalent in real-world cost to option 5 in our current single-DB architecture, simpler to reason about, and forward-compatible: option 5 is a small additive change on top once RFD 0013's DB split makes its fast path actually fire. The code surface is roughly:

```go
// In server/server.go
type Server struct {
    // … existing fields …

    // buildScorer is invoked on each /vaults to (re)derive the Scorer from
    // the current meta state. Cheap: it just reads meta keys and constructs
    // structs; the expensive work (model load) is lazy inside KronkSuggester.
    buildScorer func(ctx context.Context) (agent.Scorer, string)

    cacheMu      sync.Mutex
    cachedFp     string
    cachedScorer agent.Scorer
}

// currentScorer returns the cached Scorer when the agent fingerprint matches,
// or builds + caches a new one when it changed. The old Scorer is closed
// after a grace period if it implements the closer interface.
func (s *Server) currentScorer(ctx context.Context) agent.Scorer {
    newScorer, fp := s.buildScorer(ctx)

    s.cacheMu.Lock()
    defer s.cacheMu.Unlock()

    if s.cachedScorer != nil && fp == s.cachedFp {
        return s.cachedScorer
    }

    if s.cachedScorer != nil {
        s.log.Info("agent config changed, rebuilding scorer",
            "old", s.cachedFp, "new", fp)
        s.scheduleClose(s.cachedScorer)
    } else {
        s.log.Info("agent scorer initialised", "fingerprint", fp)
    }
    s.cachedScorer = newScorer
    s.cachedFp = fp
    return newScorer
}
```

The CLI's existing `buildScorer` (already in `cli/agent.go`) is split into two: read agent config + compute fingerprint, and build a Scorer from that config. The combined return shape `(scorer, fingerprint)` is what the server's callback expects.

`handleListVaults` calls `s.currentScorer(ctx)` instead of touching a static `s.scorer` field.

### Closing the old Scorer

The kronk backend's `KronkSuggester` holds a loaded GGUF model in memory. When the user changes the model path, we need to release the old one — but a concurrent `/vaults` request might still be using it.

Two clean options:

- **Delayed `Close` (chosen).** After a swap, the old Scorer's `Close(ctx)` runs in a goroutine that sleeps for a grace period (5 s) first. The agent's per-call timeout is 1 s, so 5 s gives 5× margin for any in-flight call to complete. ~3 lines of code.
- **`sync.WaitGroup`** counted by the handler. Cleaner if we ever observe flakes; not needed yet.

## Trade-offs accepted

- **~350 µs of meta-read overhead per `/vaults`**, even at steady state. Below the noise floor for any real workload; explicitly chosen over `data_version`-style polling because it's simpler and the cost is identical in practice.
- **Old Scorer stays loaded for an extra 5 s** after a config change. The kronk model briefly occupies memory until `Close` runs. Negligible for a single-user tool; cleaner shutdown semantics than refcounting.
- **Logging is event-only**: server logs the swap; the CLI's `buildScorer` no longer logs "agent enabled" on every call (it would be spammy). Net: one log line on first request, plus one per config change.

## Open follow-ups (not blocking)

- If we ever switch to **RFD 0013's DB split**, the meta-read on each request is still cheap; nothing in this RFD changes. The fingerprint approach composes with either DB layout.
- If we observe **flaky in-flight `/vaults` errors** during a config swap, upgrade the delayed-Close path to a `WaitGroup`-based drain. Not worth the code now.

## Status notes

- 2026-04-27 — Created and committed in the same revision. Implementation lands in this commit. The "restart `outcrop serve` to pick up the new config" hint stays in the CLI for now (e.g., `outcrop agent enable` still prints it as a fallback when the server isn't reachable), but for a running server the change takes effect on the next `/vaults` request.
- 2026-04-27 — Revised "Alternatives considered" to record an honest reconsideration of options 5 (`data_version`) and 6 (fingerprint-only). Initial draft framed `data_version` as ~350× faster at steady state, which assumed `/vaults` calls happen between writes. In outcrop's actual workload `/vaults` is almost always preceded by a `/clip` (which writes history and optionally training rows), so `data_version` lands in the slow path on virtually every call and ends up costing the same as fingerprint-only — *plus* the extra `PRAGMA` query. The `data_version` win is real but unlocks only after the RFD 0013 DB split scopes config writes away from analytics writes. Decision (option 6) unchanged; rationale corrected.

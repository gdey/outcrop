---
rfd: 0013
title: Configuration / Analytics Database Split
status: ideation
created: 2026-04-27
authors:
  - gdey
tags: [design, store, sqlite, architecture]
related: [0003, 0005, 0011, 0012]
superseded-by: ~
---

# Configuration / Analytics Database Split

## Summary

Split outcrop's single SQLite DB (currently `~/Library/Application Support/outcrop/outcrop.db`) into two:

- **`config.db`** — `vaults`, `meta`. The things the user *configured*. Small (kilobytes); meant to be backed up, restored, version-controlled, copied to a new machine.
- **`analytics.db`** — `history`, `training_examples`, future capture-time enrichment tables (RFD 0007). The things the system *observed* about the user's behaviour. Potentially large (megabytes to gigabytes for the training corpus); meant to be exported, pruned, or wiped without touching the configured state.

Read paths that need both (e.g., `outcrop training-data status` joining example counts against vault keys for display) use SQLite's `ATTACH DATABASE` to query across in a single statement.

This RFD is `ideation`. **We are deliberately not building it now.** The remainder of the document captures why, and the concrete triggers that would make us revisit.

## Motivation

The wins are real but quality-of-life rather than correctness. In rough order of weight:

### Backup story

`config.db` is what a user reasonably wants to back up: their token, vault registrations, agent settings, vault descriptions. It's small, sensitive enough to encrypt, and useful when migrating to a new machine.

`analytics.db` is what they probably *don't* want in a backup: per-domain history, possibly thousands of training examples with URLs, titles, notes, selected text. Potentially large; arguably unwanted noise in a config snapshot.

With one DB, every backup grabs the full corpus. With two, `cp config.db backup-folder/` is the obvious move and the analytics stays put.

### Privacy / cleanup ergonomics

`outcrop training-data clear` exists today; it does `DELETE FROM training_examples`. With analytics in its own file, "clear" could be `DROP TABLE` (or even `rm analytics.db`) with the same effect. Symmetrically: a future `outcrop history clear` for per-domain ranking history.

These operations are conceptually "wipe my behaviour log" — having that log be a separate file makes that mental model concrete and the implementation trivially correct.

### Future analytics scale

`history` is bounded by `(distinct registrable domain × distinct vault)` per user — small. But `training_examples` grows linearly with clip count, indefinitely. Future post-clip enrichment (RFD 0007) adds another row per clip. Embedding-based retrieval (RFD 0009) adds embedding blobs.

A heavy-clip user could end up with a multi-megabyte (eventually multi-gigabyte) `analytics.db` while their `config.db` stays well under 100 KB. Separation makes "back up the small thing" much more obvious.

### Mental model

"What did I configure?" vs. "what has the system observed?" is a real cognitive split. One file conflates them; two files surface them. Useful for a single-user tool where the user is also the operator.

### Live-reload becomes measurably faster

RFD 0012 settled on a fingerprint-on-every-request design (option 6) that costs ~350 µs per `/vaults` call. It also evaluated `PRAGMA data_version` polling (option 5) — and the two come out equivalent in cost in the *current* single-DB architecture, because `data_version` increments on every clip-time write (`RecordClip`, `RecordTrainingExample`) and `/vaults` is almost always preceded by a clip. So the `data_version` fast path almost never fires.

With the split, `RecordClip` / `RecordTrainingExample` write to `analytics.db`; they no longer perturb `config.db`'s `data_version`. Now the fast path fires for nearly every `/vaults` call, dropping the steady-state cost from ~350 µs to ~1 µs. `/vaults` for the LLM-disabled case is ~1 ms total, so this is ~30% off the request — a real, measurable win once it's available.

This is the only post-split change RFD 0012 has flagged: a small additive `PRAGMA data_version` fast path on top of the existing fingerprint logic. The fingerprint stays as defence-in-depth.

## Why we're not doing it now

Each of the wins has a counterweight. None of them are individually decisive against, but in aggregate they're enough to defer:

### The performance argument doesn't hold

The original framing was "data_version increments on every clip → `/vaults` after every clip pays a slow-path." The slow path is ~350 µs (seven `meta` reads + fingerprint compare). The full `/vaults` request, when the agent is enabled, is dominated by an LLM call of 200 ms – 1+ s. 350 µs is invisible. Splitting to "save" that overhead would be premature optimisation against an imaginary cost.

### Migration cost is real

Existing users have everything in `outcrop.db`. A split would need a one-time migration: open the old file, dump tables, write to two new files, atomically rename, drop old. Has to be safe across crashes, idempotent, and not surprising. That's RFD-grade design work for a benefit that's mostly architectural cleanliness.

### Adds operational complexity

- `outcrop config path` becomes "which one?" (or returns both).
- Anyone debugging by opening the DB with `sqlite3` now has two files to keep straight.
- Cross-DB queries via `ATTACH` are slightly slower and slightly more code than plain queries against one DB.
- Two migration directories to maintain, two `goose` runs at startup.

None of these are blocking; together they're enough drag to make "do this when there's a concrete user-visible win" the right call.

### RFD 0012 sidesteps the live-reload motivator

The fingerprint-on-every-request design doesn't care whether the DB is split. It composes with either layout. So we don't need this RFD to ship live reload.

## Concrete triggers to revisit

This RFD is parked at `ideation`, not abandoned. Promote it to `draft` when *any* of these become real:

1. **A user reports `outcrop.db` size becoming a backup nuisance.** Concrete signal: someone notes their backup tool is now grabbing 200 MB+ of analytics rows on every snapshot.
2. **Per-DB sync becomes a real ask.** E.g., the user wants to sync `config.db` between their laptop and desktop via Syncthing or iCloud, but *not* the per-domain history (which would conflict on multi-device clipping). One file conflates these; two files solve it.
3. **Distinct retention policies become real.** "Keep config forever; drop analytics rows older than 6 months" is the obvious motivator. Single-DB makes this `DELETE FROM` queries; split-DB makes it `DROP DATABASE` semantics.
4. **`training_examples` crosses ~100 k rows** (or future enrichment tables similarly bloat) and a query on the analytics side starts blocking config reads under the WAL. Sub-second concerns at that scale.
5. **External tooling reads analytics directly.** A standalone fine-tune harness pointed at `analytics.db` is cleaner than one that has to know to filter out config tables from `outcrop.db`.

If any one of these lands, revisit; otherwise the cost is paid for benefits we don't need.

## Rough scope (when promoted to `draft`)

To settle then:

1. **Migration strategy.** A startup-time check: if `outcrop.db` exists and `config.db` does not, run a one-time migration (within a transaction, with backup of the old file) before opening either.
2. **`ATTACH DATABASE` vs. two connections.** `ATTACH` lets us write joined queries when we need them (e.g., display logic that pairs `vaults.display_name` with `training_examples.actual_vault_key` counts). Two connections is more "Go" but loses cross-DB transactionality (we don't currently rely on it; history and training writes are already best-effort per RFD 0011).
3. **Schema split.** Almost certainly:
   - `config.db`: `vaults`, `meta`, `goose_db_version` (the migration tracker).
   - `analytics.db`: `history`, `training_examples`, future enrichment / embedding tables. Its own `goose_db_version`.
4. **Path layout.** Probably both files alongside each other under `os.UserConfigDir()/outcrop/`. The `OUTCROP_DB` env var becomes either deprecated (with `OUTCROP_CONFIG_DB` and `OUTCROP_ANALYTICS_DB` overrides) or made into a base directory hint.
5. **CLI surface.** `outcrop config path` returns the config DB path; new `outcrop analytics path` returns the analytics one. Or one command listing both.

## Out of scope (for this RFD when written)

- Splitting deeper (e.g., a per-vault analytics DB). Possible long-term; not motivated by anything we know about.
- Cloud sync, external storage, or any non-local persistence. Outcrop stays local-first.
- Reusing the split for multi-user separation. Single-user tool; not relevant.

## Status notes

- 2026-04-27 — Created as `ideation` after the design discussion that produced RFD 0012. Captures the alternative architecture, the wins it would unlock, the reasons we're not building it now, and the concrete triggers to revisit. Move to `draft` when any one of the triggers above materialises.
- 2026-04-27 — Strengthened the "Live-reload becomes measurably faster" section after a sharper reading of RFD 0012's cost analysis. Initial draft called the live-reload speedup "not measurably faster"; that was an understatement. Without the split, `data_version` lands in the slow path on essentially every `/vaults` call (because clip-time writes bump the version on the hot path). With the split, `data_version` only increments on real config writes — so the fast path fires for nearly every `/vaults` and the per-request cost drops from ~350 µs to ~1 µs. Modest absolute number; meaningful 30% slice of the LLM-disabled `/vaults` request budget. Still a side benefit, not a primary motivation, but worth pricing honestly.

---
rfd: 0011
title: Training-data Capture for Fine-tuning
status: ideation
created: 2026-04-27
authors:
  - gdey
tags: [design, server, llm, agent, training, fine-tuning]
related: [0001, 0003, 0005]
superseded-by: ~
---

# Training-data Capture for Fine-tuning

## Summary

Capture each clip as a training example: the page (URL + title + selected text), the candidate vault list with descriptions at the moment of capture, the LLM's suggestion (when the agent ran), and the vault the user actually picked. Stored locally; opt-in. Exposed via `outcrop training-data export` for use by an external fine-tuning pipeline. The dataset feeds a follow-up fine-tune of the user's local model so it learns *this user's* routing preferences.

## Motivation

A generic small chat model picks vaults reasonably from URL + title but plateaus quickly: it doesn't know that this particular user routes most ML papers to "Reading List" and only puts Anthropic / OpenAI / DeepMind papers in "Work Notes." That distinction is in the user's head and in their *behaviour*. Every clip the user actually saves is implicit feedback — the gap between "what would the model have picked" and "what did they pick" is supervised training data.

Capturing that gap is cheap (one small DB row per clip). Acting on it is downstream — an external fine-tune run that produces a new GGUF the user points outcrop at via `outcrop agent enable --model ...`. **This RFD is the *capture* surface only.** Fine-tuning itself is a separate toolchain and out of scope here.

## Constraints already agreed

- **RFD 0005 §Auto-route** — the pre-clip Suggester and the capture-time Refiner are the natural emit points for "suggested vault." Their existing internal logging is informational; this RFD turns the same signal into structured rows.
- **RFD 0001 §"No cloud, no account"** — local-only. The dataset never leaves the user's machine unless they explicitly export and move it.
- **RFD 0003 §Logging** — the project is conservative about what it persists. Training-data capture is an opt-in widening; default `disabled`.

## Rough scope

To settle when this moves to `draft`:

1. **Storage shape.** New `training_examples` table in the same SQLite DB. Denormalised so an example is self-contained even after a vault is renamed or deleted:

   | Field | Notes |
   |---|---|
   | `id` | autoincrement |
   | `ts` | unix seconds, UTC |
   | `mode` | `"none"` (agent off, manual pick), `"preclip"` (popup pill ordering), or `"auto"` (Auto-route) |
   | `url`, `title`, `selected_text`, `notes` | exactly what the prompt would have seen |
   | `candidate_vaults` | JSON array of `{key, displayName, description}` at capture time |
   | `suggested_vault_key` | LLM's top pick, NULL if no LLM ran |
   | `actual_vault_key` | what was written to (may be Refiner's pick, default-vault fallback, or Scorer-ranking fallback per RFD 0005) |
   | `auto_routed` | bool — whether the request set `autoRoute=true` |
   | `note_path`, `image_path` | relative to the vault, point at the on-disk artefact |
   | `image_sha256` | for dedup / cross-reference; the bytes themselves stay in the vault |

   No FK to `vaults` — examples must survive vault deletion.

2. **Capture trigger.** On every successful `POST /clip`. Three row shapes:
   - User picked a vault, agent off → `mode="none"`, `suggested=NULL`.
   - User picked a vault, agent on (popup pill ordered by LLM nudge) → `mode="preclip"`, `suggested=<LLM top>`.
   - User picked Auto, server LLM-routed → `mode="auto"`, `suggested=<Refiner pick>`, `actual=<final pick after fallback chain>`.

3. **Opt-in.** Default disabled. New `meta` key `training_data_enabled`. CLI:
   ```
   outcrop training-data enable          # prints what gets captured before turning on
   outcrop training-data disable
   outcrop training-data status
   outcrop training-data list [--limit N] [--since DATE]
   outcrop training-data export [--format jsonl|csv] [--output PATH] [--since DATE] [--vault KEY] [--include-images]
   outcrop training-data prune [--before DATE] [--keep-last N]
   ```

4. **Export format.**
   - JSONL by default — one example per line, ML pipeline friendly.
   - CSV for spreadsheet review.
   - `--include-images` inlines base64 PNGs at export time. The DB only holds paths; export decides whether to materialise the bytes.

5. **Retention.** No automatic policy; users manage size with `prune`. `disable` stops new rows but doesn't delete existing.

6. **Schema migration.** New file (likely `00003_training_examples.sql`). Lands ahead of the capture code that writes to it.

## Privacy

The training table holds sensitive fields: URLs, titles, notes typed by the user, selected page text. Posture:

- **Disabled by default.** `enable` is an explicit opt-in, prints what gets captured first.
- **Same DB.** Stored alongside vaults / history / meta; `outcrop config path` shows the file.
- **No auto-export.** Outcrop never uploads anything. Export is explicit, user-driven, and writes to a file path the user provides.
- The dataset contains roughly the same content the user already trusts to disk in vaults — but in concentrated, machine-readable form. The README enable-time message says exactly that.

## Out of scope (for this RFD when written)

- Running fine-tuning. Outcrop produces examples; an external toolchain (kronk's training tooling, llama.cpp scripts, a notebook) consumes them.
- Sharing the dataset across users / cross-user aggregation. Single-user, on-device only.
- Reward modelling / RLHF flavours. Plain supervised pairs of (input, chosen vault).
- A UI for browsing / curating the dataset. CLI is enough for v1; `training-data list` is the read path.
- Online fine-tuning that updates the running model in place. Out-of-band only.
- Capturing arbitrary ad-hoc events outside the clip path (e.g., vault renames, manual reroutings). v1 captures clip events only.

## Status notes

- 2026-04-27 — Created as `ideation`. Spawned by the observation that the agent's accept/override behaviour is implicit training data worth capturing while RFD 0005 ships. Move to `draft` once RFD 0005 is in users' hands and the capture surface is concrete enough to design against real Suggester / Refiner outputs.

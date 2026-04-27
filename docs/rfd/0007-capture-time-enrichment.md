---
rfd: 0007
title: Capture-time Enrichment
status: ideation
created: 2026-04-27
authors:
  - gdey
tags: [design, server, llm, agent, markdown]
related: [0001, 0003, 0005]
superseded-by: ~
---

# Capture-time Enrichment

## Summary

Once the clip is on disk, a follow-up LLM pass annotates the note: proposes tags, writes a one-line TL;DR, possibly improves the title. Modifies the markdown front-matter and/or body in place. Distinct from the *routing* decision Auto-route (RFD 0005) already makes — enrichment never moves a clip between vaults; it only edits the file.

## Motivation

The clip lands as a stable artefact: a markdown note plus an image, in the right vault. That's the foundation. But the note as written is sparse — title is the page title, body is the user's notes plus the embedded image, no tags, no summary. Obsidian's value comes from cross-linking, and sparse notes don't link well. An enrichment pass closes the gap without asking the user to do extra work.

## Constraints already agreed

- **RFD 0005 §Auto-route** — routing is the agent's first job. Enrichment never re-routes.
- **RFD 0001 §Future: local agent integration** — the contract is server ↔ agent; the extension is not involved.
- **RFD 0003 §Clip writing** — clips on disk are written by `clip.Write`; that writer (or the layer immediately above it) is the seam where enrichment plugs in.

## Rough scope

To settle when this moves to `draft`:

1. **Sync vs async.** Does `POST /clip` block until enrichment lands, or does it return immediately with a sparse note that gets rewritten in a follow-up pass? Async is friendlier (snappy save, enrichment in the background) but introduces "the file just changed" state for any tool watching the vault. Sync is simpler but adds a latency budget the user is waiting on.
2. **What gets enriched, in v1.**
   - **Tags** in YAML front-matter (`tags: [...]`).
   - **TL;DR** as a section near the top of the body.
   - **Title rewrite** — replace the auto-generated H1 with something better.
   - **Source metadata** in front-matter (already partial today).
   Pick which of these v1 ships, which are deferred again.
3. **Prompt and output shape.** Routing prompts return one short string. Enrichment returns *structured* data — list of tags, optional summary string, optional title. JSON mode where the backend supports it; a strict prose format with a parser otherwise.
4. **Idempotency.** If the user has already edited the note (added their own tags, rewritten the title), enrichment must not clobber. Either: detect human edits and skip; or: enrich exactly once on creation and never again; or: append-only enrichment that adds tags without removing.
5. **Failure handling.** Enrichment is best-effort by definition — the clip already saved successfully. Failures log WARN and leave the note as-is. No user-facing error.
6. **Model selection.** Routing wants a small fast model; enrichment can tolerate more latency for better quality. Run on the same configured model, or allow a separate enrichment-specific model? One model for v1 with a knob to override later.
7. **Cancellation and crash recovery.** If the server restarts mid-enrichment, the partially-written note is on disk. Recover gracefully (re-run? leave alone? mark "in-progress" in front-matter?) or accept the rare bad state.
8. **Per-vault opt-out.** Some vaults are "raw clip in, hand-edit later" workflows; others want enrichment. A `vault.enrich` boolean column would let users pick per vault.

## Out of scope (for this RFD when written)

- Note linking ("see also" suggestions across notes). That's a different problem with its own retrieval story (RFD 0009).
- Re-enriching old notes on demand (a hypothetical `outcrop reenrich` CLI). Possible, but not on the hot path.
- Embedding-based "is this a duplicate" check at write time. Belongs to a future de-dup RFD.

## Status notes

- 2026-04-27 — Created as `ideation`. Spawned by RFD 0005 §"Deferred". Move to `draft` after RFD 0005 has shipped and we've seen what users want from the auto-saved notes.

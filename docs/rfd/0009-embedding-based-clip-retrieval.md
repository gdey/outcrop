---
rfd: 0009
title: Embedding-based Per-vault Clip Retrieval
status: ideation
created: 2026-04-27
authors:
  - gdey
tags: [design, server, llm, embeddings, search]
related: [0001, 0003, 0005, 0008]
superseded-by: ~
---

# Embedding-based Per-vault Clip Retrieval

## Summary

Read-side semantic search across saved clips. Compute and store an embedding for each clip on write; on query, embed the user's query and find the closest clips by cosine similarity. Answers "find the clip about sourdough I saved in spring" without exact keywords, and is the substrate for future "see also" suggestions inside notes.

## Motivation

The point of clipping into Obsidian is to find things again later. Obsidian itself does keyword search well, but doesn't help when the user remembers the gist but not the words. A small embeddings index over the saved clips makes "the recipe with rye flour" find the right note even if the page didn't say "rye." A cheap addition once the embedding lane (RFD 0008) exists.

## Constraints already agreed

- **RFD 0005 §Clip writing** — clip writes are the seam where new embedding work plugs in.
- **RFD 0008** (when written) — the embedding model lane is shared with vault routing; we don't build a second one for retrieval.
- **RFD 0001** — local-only. Both the index and the model live on the user's machine.

## Rough scope

To settle when this moves to `draft`:

1. **What gets embedded per clip.**
   - Title + body (cheap, mostly sufficient).
   - Image (only with a vision-capable embedding model — different lane).
   - Selected text — already in the body, redundant.
   Probably title + body for v1.
2. **Index location.**
   - Central SQLite DB alongside `vaults`/`history` — single source of truth, easy to back up.
   - Per-vault sidecar files — lets the user move a vault elsewhere with its index. More code paths.
   Centralised is simpler.
3. **Index size.** Embeddings are 384–1536 floats per clip depending on model. At 1000 clips × 768 floats × 4 bytes ≈ 3 MB. Negligible.
4. **Query API.**
   - CLI: `outcrop search <query> [--vault KEY] [--limit N]`.
   - HTTP: a new `GET /search?q=&vault=&limit=` endpoint behind the same bearer auth.
   - Extension surface: maybe later. Out of scope for v1 of retrieval.
5. **Lifecycle.**
   - On `POST /clip` write: enqueue embed (sync or async — probably async to keep saves snappy).
   - On note edit (outside outcrop): we don't see the change. Either re-embed on a schedule, or rely on a `outcrop reindex` CLI.
   - On note delete (outside outcrop): index drift. Same answer.
   - On vault delete: cascade-drop embeddings (FK like `history`).
6. **Schema.** New `clip_embeddings` table: `(vault_key, note_path, embedding BLOB, indexed_at, model_identity)`. Drop and rebuild rows when `model_identity` doesn't match the current configured embedding model.

## Out of scope (for this RFD when written)

- Live "see also" suggestions inside notes — separate UX problem; uses retrieval as input.
- Cross-vault clustering or "you should consolidate these vaults" advice.
- Reindexing the entire clip library on demand from the extension. CLI-driven for v1.
- Image-content search via vision-embedding models. Possible follow-up.

## Status notes

- 2026-04-27 — Created as `ideation`. Spawned by RFD 0005 §"Deferred". Tag for v2; depends on RFD 0008 (embedding lane) being in place.

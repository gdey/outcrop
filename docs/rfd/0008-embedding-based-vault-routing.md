---
rfd: 0008
title: Embedding-based Vault Routing
status: ideation
created: 2026-04-27
authors:
  - gdey
tags: [design, server, llm, agent, embeddings]
related: [0003, 0005]
superseded-by: ~
---

# Embedding-based Vault Routing

## Summary

Replace (or augment) RFD 0005's chat-completions vault scorer with cosine similarity over embeddings: a vector for each vault's description, a vector for the page being clipped, closest match wins. Cheaper, more deterministic, can run on a much smaller model than chat-completion ranking. Pulls in real infrastructure — a vector store with lifecycle hooks — that RFD 0005 was deliberate about not building yet.

## Motivation

RFD 0005 chose chat completions to validate the routing-helps hypothesis cheaply. If chat-rank works well enough we may never need this RFD. If it's slow, expensive in model size, or non-deterministic in ways that hurt UX, embeddings are the next step:

- **Lower latency.** A small embedding model produces a vector in tens of milliseconds; chat completions take hundreds.
- **Smaller required model.** Embedding models like `bge-small-en` or `all-MiniLM-L6-v2` are 25–100 MB; chat models for routing are 2 GB+.
- **Deterministic.** Same input, same output. Easier to debug, easier to test, easier to A/B against.
- **Multi-rank with confidence.** Cosine similarity gives a score per vault, not just a top-1; the popup pill could show confidence (UX work belongs in RFD 0006).

## Constraints already agreed

- **RFD 0005 §"Schema and vault descriptions"** — `vault.description` exists. Required input here; display names alone are too thin to embed meaningfully.
- **RFD 0005 §Open decision #2** — chat completions is the v1 path. Embeddings replaces *or augments* it; doesn't supersede until the bake-off proves it.
- **RFD 0001** — local-only. The embedding model runs on the user's machine alongside any chat model.

## Rough scope

To settle when this moves to `draft`:

1. **Augment or replace?** Three options:
   - Embedding-only ranking (fast, deterministic; can be wrong without semantic richness chat brings).
   - Chat-only ranking (today's RFD 0005 path).
   - Two-stage: embedding picks top-K candidates, chat reranks.
   Two-stage is probably the right answer for accuracy + latency, but it's more complex.
2. **Embedding model lane.** Separate from chat? Some chat models expose embeddings. For kronk SDK, GGUF embedding models load alongside or in place of chat models. For HTTP, `/v1/embeddings` is OpenAI-compatible (ollama supports it; others vary). New `agent_embedding_model` config key, parallel to `agent_model_path` / `agent_model`.
3. **Vector store.**
   - In-memory only (recompute on startup; cheap when there are <100 vaults).
   - Persisted in SQLite as `BLOB` columns on `vaults` (zero new dependencies).
   - SQLite extension like `sqlite-vec` for `MATCH`-style query (extra dependency, faster at scale we don't have).
   In-memory or SQLite-blob is plenty for a single-user tool.
4. **Lifecycle.** Re-embed on:
   - Vault add (compute description embedding once).
   - Vault description change.
   - Embedding-model change (full rebuild — new `meta` key tracks the model identity).
   - Vault rename — *no*, name isn't embedded; description is.
5. **Page embedding input.** Title only? Title + URL? Title + URL + a small page-text excerpt? Page-text widens privacy concerns and requires the extension to extract it (it doesn't today).
6. **Threshold.** "No vault was a good match" → fall through to RFD 0005's chat scorer or directly to history. Configurable but with a sensible default; needs a small dataset to tune against.
7. **Schema delta.** A `vaults.description_embedding BLOB` column, plus a `meta` key for the embedding model identity (so we know when to rebuild).

## Out of scope (for this RFD when written)

- Read-side semantic search across saved clips — that's RFD 0009 (per-vault retrieval).
- Embedding-based duplicate detection at write time.
- Cross-vault clustering / "you should split this vault" advice.

## Status notes

- 2026-04-27 — Created as `ideation`. Spawned by RFD 0005 §"Deferred". Move to `draft` once RFD 0005 has shipped and we have evidence about whether chat-rank quality or latency is the bottleneck.

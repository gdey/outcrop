---
rfd: 0010
title: Multi-shot / Agentic Flows
status: ideation
created: 2026-04-27
authors:
  - gdey
tags: [design, server, llm, agent, agentic]
related: [0001, 0005, 0007]
superseded-by: ~
---

# Multi-shot / Agentic Flows

## Summary

A capability seam for the LLM to make *multiple* coordinated calls during one user-facing operation: fetch the page text, summarise it, decide a vault, propose tags — all in one chain. v1 of the agent (RFD 0005) is single-shot; this RFD is the future expansion path when single-shot's quality plateaus.

## Motivation

Single-shot routing on URL + title is cheap but thin. Single-shot Auto-route (RFD 0005) gets the image + notes too, which is richer but still one prompt. Some real wins probably need more:

- **Page-text-aware routing.** "This article is about X" is hard to read off a Twitter title. Fetching the linked content, summarising, then routing would clearly win on noisy pages — at much higher latency and complexity.
- **Disambiguation.** When two vaults look equally good, asking the LLM "what would distinguish them?" then re-prompting with that distinction is a pattern single-shot can't do.
- **Cross-step caching.** Summarise once, use the summary for routing, tagging, and TL;DR. Single-shot redundantly re-derives summary-ish facts on each call.

## Constraints already agreed

- **RFD 0005** — single-shot is the v1 path. Multi-shot is opt-in and on top of, not in place of.
- **RFD 0001** — local-only. Anything the agent does happens on the user's machine.
- **RFD 0007** (when written) — enrichment is a per-clip post-processing pass. A multi-shot framing could unify routing + enrichment into one chain instead of two passes.

## Rough scope

To settle when this moves to `draft`:

1. **Smallest useful multi-shot.** Likely "fetch the page text, summarise, then route + enrich in a single follow-up call." Two LLM calls and one HTTP fetch. Define interfaces around exactly that and grow from there — don't build a generic agent framework.
2. **Tooling vs hand-rolled.** Frameworks (langchaingo, eino, in-house mini-orchestrator) trade abstraction for control. For local single-user scope, hand-rolled is probably right; revisit if the chain grows past 3–4 steps.
3. **Web fetch.** The server gains permission to fetch arbitrary URLs (the page being clipped). Limits to honour: only the URL the extension just gave us; respect robots.txt? (probably not — single user, low rate); timeout; size cap on the fetched body.
4. **Latency budget.** Auto-route is already at the user's "click Save and wait" boundary. Adding 2–10 s of multi-shot pass to it pushes past patience. Likely candidates run async after the clip is saved (RFD 0007's enrichment seam).
5. **Failure surfacing.** Multi-shot multiplies failure modes. Each step needs its own fallback. Cleanest: chain steps as a pipeline that can shed late steps gracefully (got the summary but not the tags → save with summary, skip tags).
6. **Privacy delta.** Page-text fetch widens what reaches the LLM versus the page-image-only path RFD 0005 documents. Needs explicit user opt-in beyond "agent is enabled."

## Out of scope (for this RFD when written)

- A general-purpose agentic harness with arbitrary tools. Outcrop is not that.
- LLM-driven "use Obsidian's plugin API to do thing X." Outcrop writes files; Obsidian reads them. Stays that way.
- Background-running agents that make decisions without a triggering clip. The agent runs in response to user clip actions, full stop.

## Status notes

- 2026-04-27 — Created as `ideation`. Spawned by RFD 0005 §"Deferred". Way out on the horizon — won't move to `draft` until RFD 0005 + RFD 0007 are in users' hands and we have evidence single-shot is the bottleneck for real workflows.

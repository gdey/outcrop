---
rfd: 0006
title: Agent Capabilities and Status
status: ideation
created: 2026-04-27
authors:
  - gdey
tags: [design, server, llm, agent, capabilities]
related: [0001, 0004, 0005]
superseded-by: ~
---

# Agent Capabilities and Status

## Summary

A capabilities and status surface for the local-LLM agent introduced in RFD 0005. v1 of the agent settles for "always show 'Auto' when the agent is enabled," which works but degrades silently when the user runs Auto on a text-only model. This RFD covers the next iteration: a `GET /agent/status` endpoint plus a small capability declaration system the extension can use to gate features intelligently (Auto only when vision is available; future capabilities surfaced as they're added).

## Motivation

RFD 0005 punted on two questions that are conjoined and worth their own design:

- **Vision-capability detection** (RFD 0005 §Open decisions #6). Always-show is fine for v1, but Auto on a text-only model is silently worse than the heuristic alone. A capability check would let the extension hide the entry, soften it, or route through a fallback prompt that doesn't pretend the model can see.
- **Richer agent status** (RFD 0005 §Open decisions #7). The hot-path popup gets `agent.enabled` from the extended `GET /vaults` response (one trip, minimum information). Anything richer — backend version, model name, capability list, local vs remote — wants its own endpoint *and* its own design discussion.
- **UI surfacing of agent state** (RFD 0005 §Open decisions #3). v1 deliberately doesn't decorate the vault pill — no "suggested by agent" badge, no capability hints, no locality indicators. The question of *what*, if anything, to surface to the user about the agent is downstream from "what does the server know about its own capabilities," and belongs here rather than in 0005.

Bundling these into one RFD is correct: capability *declaration*, capability *exposure*, and the UX of *surfacing* capabilities are the same problem viewed from three angles.

## Constraints already agreed (RFD 0005)

- The agent surface is consumed by the extension, not the user. The user-visible toggle stays `outcrop agent enable / disable`.
- v1 of the agent always shows "Auto" when enabled — the work in this RFD changes that to a capability-aware decision, not an unconditional render.
- `GET /vaults` carries `agent.enabled` only. Anything richer goes in this RFD's endpoint, not on the hot path.

## Rough scope

To settle when this RFD moves to `draft`:

1. **Endpoint shape.** Likely something like:

   ```
   GET /agent/status → 200
   {
     "enabled": true,
     "backend": "kronk" | "http",
     "local":   true,
     "version": "kronk 0.x.y" | "ollama 0.x.y" | "unknown",
     "model":   "llama-3.2-3b-instruct.Q4_K_M.gguf",
     "capabilities": ["chat", "vision", "embeddings"]
   }
   ```

   When `enabled` is `false` the rest of the fields may be absent.

2. **Capability declaration.** The set is small and known: `chat`, `vision`, `embeddings`, possibly `function_calling` later. Backends declare per-model what they support — the kronk backend by inspecting the loaded GGUF metadata; the HTTP backend by either probing the endpoint's `/v1/models` or by the user tagging their endpoint config explicitly. Probing is unreliable across backends; explicit declaration is more honest. Compromise: probe where we can, fall back to a config-time tag.

3. **Auto-routing gating.** When the user picks Auto and `vision` is not in `capabilities`, what happens? Options:
   - Render Auto anyway, server sends a text-only prompt (today's v1 behaviour).
   - Hide Auto entirely; vision-required mode.
   - Render Auto but with a tooltip / sub-label noting "text-only — picks based on title and notes."
   The last is probably the right answer for v2.

4. **Local vs remote.** A `local` boolean drives the existing RFD 0005 config-time warning (the one that widens its language when `agent_endpoint` points at a non-loopback host) and gives the popup a way to surface "this is local" iconography if we want it. Computed: kronk backend → always local; HTTP backend → check whether the endpoint host is loopback.

5. **Caching.** Status doesn't change between requests except across `outcrop agent enable / disable` and a server restart. Extension can cache for a popup session; server doesn't need to.

6. **Auth.** Same bearer token as everything else. `/agent/status` is just another auth-required route.

7. **CORS and headers.** Same posture as `/vaults` — no extension-specific CORS rules required.

## Out of scope (for this RFD when written)

- Anything that *changes* what the server does. Capability discovery only.
- Per-vault capabilities (a vault doesn't have its own LLM).
- A general server-status endpoint. `/agent/status` is agent-specific; if we ever want a broader server-status surface, that's another RFD.
- Deciding the answer to question (3) above before we have v1 of the agent in users' hands and have seen what the failure modes actually look like.

## Status notes

- 2026-04-27 — Created as `ideation`. Spawned by RFD 0005 open decisions #6 and #7. Move to `draft` once v1 of the agent (RFD 0005) is in users' hands and we have evidence about which capability gates actually matter in practice.

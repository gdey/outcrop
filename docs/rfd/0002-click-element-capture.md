---
rfd: 0002
title: Click-Element Capture Mode
status: ideation
created: 2026-04-26
authors:
  - gdey
tags: [design, firefox-extension, ux]
related: [0001]
superseded-by: ~
---

# Click-Element Capture Mode

## Summary

A second selection mode for the Outcrop Firefox extension where the user clicks a single element on the page (article body, card, blockquote, code sample, etc.) and the extension captures the bounding rectangle of that element. This complements the v1 drag-rectangle mode (RFD 0001) without replacing it.

## Motivation

Drag-rectangle is universally understood but fiddly when the user wants exactly "this article" or "this card." On well-structured pages, click-element gives cleaner captures with less effort — the user expresses intent ("this thing"), and the extension figures out the geometry.

It is, however, a power-user mode: it requires the user to understand that they're picking *an element* (not a region), and the result depends on the page's DOM, which they cannot see.

## Constraints already agreed (RFD 0001)

- **Drag-rectangle is the default.** Click-element is opt-in.
- **Modifier key:** holding **Alt/Option** activates click-element mode.
- **Visible toggle:** the capture overlay shows a toggle button for switching modes. When Alt is held, the toggle visibly reflects the active state (so the keyboard shortcut and the button stay in sync visually).
- **Server contract is unchanged.** The extension still produces a rectangle (and a cropped image) and POSTs to `/clip`. The server cannot tell, and does not care, which mode produced the rectangle.

## Rough scope

Open design questions to settle when this RFD moves to `draft`:

1. **What counts as "the element"?** The deepest hovered node, or a heuristic that walks up to the nearest semantic block (`<article>`, `<section>`, common container patterns)? Naive deepest-node selection picks `<span>`s inside paragraphs — almost never what the user wants.
2. **Hover highlighting.** Outline style, color, z-index, behavior over `pointer-events: none` regions.
3. **Element refinement.** Some screenshot tools let the user tap a key to "expand" the selection to the parent element. Do we want that? It mostly papers over a bad heuristic in (1).
4. **Pages that fight back.** Sites with overlays, modals, sticky headers, or aggressive event handlers. How much do we work around vs. let degrade?
5. **Image extraction.** The geometry-from-DOM approach raises a question: do we still capture via `tabs.captureVisibleTab()` and crop, or is there a path that uses the DOM more directly (e.g., `html2canvas`)? Trade-offs around fidelity, fonts, dynamic content, and CSP.
6. **Modifier behavior across platforms.** Alt on Linux/Windows, Option on macOS. Are there platforms or keyboard layouts where Alt is unsuitable?

## Out of scope (for this RFD when written)

- Anything that changes the server contract.
- Multi-element selection (rubber-band over a list of elements).
- Capture of off-screen content (long-page screenshot stitching).

## Status notes

- 2026-04-26 — Created as `ideation` placeholder. Move to `draft` when v1 is shipped and we're ready to invest in the click-element experience.

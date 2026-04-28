// content.ts — injected on demand via chrome.scripting.executeScript.
//
// Two phases of UI mounted in a closed shadow root so page CSS can't bleed in:
//   1. Drag overlay: user draws a rectangle.
//   2. Preview card: image + notes textarea + Save/Cancel.
//
// All messaging goes via chrome.runtime.sendMessage / onMessage to the
// background service worker. The script is idempotent — re-injecting cleans up
// any prior instance before mounting a new one.

import type {
  CancelMessage,
  CroppedMessage,
  Message,
  RectMessage,
  SaveMessage,
} from "./lib/messages";

const ROOT_ID = "__outcrop-overlay__";
const PRIOR_KEY = "__outcropTeardown";

type WithTeardown = { [PRIOR_KEY]?: () => void };

(function main(): void {
  // Tear down any prior instance (covers re-injection).
  (globalThis as unknown as WithTeardown)[PRIOR_KEY]?.();

  const selectedText = window.getSelection()?.toString() ?? "";
  const dpr = window.devicePixelRatio || 1;

  const root = document.createElement("div");
  root.id = ROOT_ID;
  root.style.cssText = "all: initial; display: contents";
  document.body.appendChild(root);
  const shadow = root.attachShadow({ mode: "closed" });
  shadow.appendChild(makeStyles());

  const ac = new AbortController();

  function teardown(): void {
    ac.abort();
    root.remove();
    delete (globalThis as unknown as WithTeardown)[PRIOR_KEY];
  }

  (globalThis as unknown as WithTeardown)[PRIOR_KEY] = teardown;

  function sendCancel(): void {
    const m: CancelMessage = { type: "cancel" };
    chrome.runtime.sendMessage(m).catch(() => {});
    teardown();
  }

  document.addEventListener(
    "keydown",
    (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        sendCancel();
      }
    },
    { capture: true, signal: ac.signal },
  );

  // Phase 1: drag overlay.
  const dragLayer = document.createElement("div");
  dragLayer.className = "drag-layer";
  shadow.appendChild(dragLayer);

  let dragStart: { x: number; y: number } | null = null;
  let rectEl: HTMLDivElement | null = null;
  let sizeEl: HTMLDivElement | null = null;

  dragLayer.addEventListener(
    "pointerdown",
    (e) => {
      e.preventDefault();
      dragStart = { x: e.clientX, y: e.clientY };
      rectEl = document.createElement("div");
      rectEl.className = "rect";
      dragLayer.appendChild(rectEl);
      sizeEl = document.createElement("div");
      sizeEl.className = "size";
      dragLayer.appendChild(sizeEl);
      dragLayer.setPointerCapture(e.pointerId);
    },
    { signal: ac.signal },
  );

  dragLayer.addEventListener(
    "pointermove",
    (e) => {
      if (!dragStart || !rectEl || !sizeEl) return;
      const x = Math.min(dragStart.x, e.clientX);
      const y = Math.min(dragStart.y, e.clientY);
      const w = Math.abs(e.clientX - dragStart.x);
      const h = Math.abs(e.clientY - dragStart.y);
      rectEl.style.left = `${x}px`;
      rectEl.style.top = `${y}px`;
      rectEl.style.width = `${w}px`;
      rectEl.style.height = `${h}px`;
      sizeEl.textContent = `${w} × ${h}`;
      sizeEl.style.left = `${x}px`;
      sizeEl.style.top = `${Math.max(0, y - 22)}px`;
    },
    { signal: ac.signal },
  );

  dragLayer.addEventListener(
    "pointerup",
    async (e) => {
      if (!dragStart) return;
      const x = Math.min(dragStart.x, e.clientX);
      const y = Math.min(dragStart.y, e.clientY);
      const w = Math.abs(e.clientX - dragStart.x);
      const h = Math.abs(e.clientY - dragStart.y);
      dragStart = null;

      if (w < 4 || h < 4) {
        rectEl?.remove();
        rectEl = null;
        sizeEl?.remove();
        sizeEl = null;
        return;
      }

      // Hide the drag overlay so it doesn't end up in the screenshot. Wait two
      // animation frames to ensure the page has fully repainted before the
      // background calls captureVisibleTab.
      dragLayer.style.display = "none";
      await new Promise<void>((r) =>
        requestAnimationFrame(() => requestAnimationFrame(() => r())),
      );

      const msg: RectMessage = {
        type: "rect",
        rect: { x, y, w, h, dpr },
        url: window.location.href,
        title: document.title,
        selectedText,
      };
      try {
        await chrome.runtime.sendMessage(msg);
      } catch (err) {
        console.error("[outcrop] sendMessage(rect) failed", err);
        teardown();
      }
      // Phase 2 mounts when "cropped" arrives via runtime.onMessage.
    },
    { signal: ac.signal },
  );

  chrome.runtime.onMessage.addListener((msg: Message) => {
    if (msg.type === "cropped") {
      mountPreview(msg);
    }
    return undefined;
  });

  // Phase 2 lives inside an iframe rather than the shadow DOM so the page's
  // own keyboard listeners (YouTube's space-to-play, Twitter's j/k/l, sites
  // that stopImmediatePropagation in document-capture, etc.) can't intercept
  // or swallow the user's typing in the notes textarea. Iframe documents are
  // separate browsing contexts; their keyboard events don't reach the parent.
  function mountPreview(msg: CroppedMessage): void {
    dragLayer.remove();

    const iframe = document.createElement("iframe");
    iframe.title = "Outcrop preview";
    iframe.style.cssText = `
      position: fixed;
      inset: 0;
      width: 100vw;
      height: 100vh;
      border: 0;
      background: transparent;
      z-index: 2147483647;
    `;

    iframe.addEventListener(
      "load",
      () => {
        const idoc = iframe.contentDocument;
        if (!idoc) return;
        populatePreview(idoc, msg);
      },
      { once: true, signal: ac.signal },
    );

    shadow.appendChild(iframe);
  }

  function populatePreview(idoc: Document, msg: CroppedMessage): void {
    const styleEl = idoc.createElement("style");
    styleEl.textContent = PREVIEW_CSS;
    idoc.head.appendChild(styleEl);

    const card = idoc.createElement("div");
    card.className = "preview-card";

    const chip = idoc.createElement("span");
    chip.className = "vault-chip";
    chip.textContent = `→ ${msg.vaultName}`;

    const img = idoc.createElement("img");
    img.src = `data:image/png;base64,${msg.imageBase64}`;
    img.alt = "captured region";

    const textarea = idoc.createElement("textarea");
    textarea.className = "notes";
    textarea.placeholder = "Notes (optional)…";

    const cancelBtn = idoc.createElement("button");
    cancelBtn.type = "button";
    cancelBtn.className = "cancel";
    cancelBtn.textContent = "Cancel";

    const saveBtn = idoc.createElement("button");
    saveBtn.type = "button";
    saveBtn.className = "save";
    saveBtn.textContent = "Save";

    const actions = idoc.createElement("div");
    actions.className = "actions";
    actions.appendChild(cancelBtn);
    actions.appendChild(saveBtn);

    card.appendChild(chip);
    card.appendChild(img);
    card.appendChild(textarea);
    card.appendChild(actions);
    idoc.body.appendChild(card);

    const save = (): void => {
      const out: SaveMessage = { type: "save", notes: textarea.value };
      chrome.runtime.sendMessage(out).catch((e) => console.error(e));
      teardown();
    };

    saveBtn.addEventListener("click", save, { signal: ac.signal });
    cancelBtn.addEventListener("click", sendCancel, { signal: ac.signal });

    textarea.addEventListener(
      "keydown",
      (e) => {
        if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
          e.preventDefault();
          save();
        }
      },
      { signal: ac.signal },
    );

    // Escape inside the iframe cancels. The outer document's Escape listener
    // doesn't fire here because iframe events don't bubble to the parent.
    idoc.addEventListener(
      "keydown",
      (e) => {
        if (e.key === "Escape") {
          e.preventDefault();
          sendCancel();
        }
      },
      { capture: true, signal: ac.signal },
    );

    setTimeout(() => textarea.focus(), 0);
  }
})();

// Drag-overlay styles, scoped to the closed shadow root.
function makeStyles(): HTMLStyleElement {
  const s = document.createElement("style");
  s.textContent = `
    :host { all: initial; }

    .drag-layer {
      position: fixed;
      inset: 0;
      z-index: 2147483647;
      background: rgba(0, 0, 0, 0.25);
      cursor: crosshair;
    }
    .rect {
      position: fixed;
      border: 1px solid #fff;
      box-shadow: 0 0 0 1px rgba(0, 0, 0, 0.4);
      pointer-events: none;
    }
    .size {
      position: fixed;
      padding: 2px 6px;
      background: rgba(0, 0, 0, 0.7);
      color: #fff;
      font: 11px/1.4 system-ui, -apple-system, sans-serif;
      border-radius: 3px;
      pointer-events: none;
    }
  `;
  return s;
}

// Preview-card styles, injected into the iframe's own document so they're
// isolated from the host page and from the shadow root above.
const PREVIEW_CSS = `
    html, body {
      margin: 0;
      height: 100%;
    }
    body {
      background: rgba(0, 0, 0, 0.5);
      display: flex;
      align-items: center;
      justify-content: center;
      font: 13px/1.4 system-ui, -apple-system, sans-serif;
      color: #1c1c1c;
    }
    .preview-card {
      background: #fff;
      border-radius: 8px;
      padding: 16px;
      max-width: 90vw;
      max-height: 90vh;
      display: flex;
      flex-direction: column;
      gap: 12px;
      box-shadow: 0 8px 32px rgba(0, 0, 0, 0.3);
      font: 13px/1.4 system-ui, -apple-system, sans-serif;
      color: #1c1c1c;
    }
    .vault-chip {
      align-self: flex-start;
      padding: 3px 10px;
      background: #eef3f8;
      border: 1px solid #c8d8e8;
      border-radius: 12px;
      font-size: 12px;
      color: #1c4a72;
    }
    .preview-card img {
      max-width: 90vw;
      max-height: 60vh;
      display: block;
      object-fit: contain;
      background: #f3f3f3;
      border-radius: 4px;
    }
    .preview-card .notes {
      width: 100%;
      min-height: 80px;
      max-height: 200px;
      padding: 8px 10px;
      font: inherit;
      box-sizing: border-box;
      border: 1px solid #c9c9c9;
      border-radius: 4px;
      resize: vertical;
    }
    .preview-card .notes:focus {
      outline: none;
      border-color: #4682b4;
      box-shadow: 0 0 0 3px rgba(70, 130, 180, 0.18);
    }
    .preview-card .actions {
      display: flex;
      justify-content: flex-end;
      gap: 8px;
    }
    .preview-card button {
      padding: 8px 16px;
      font: inherit;
      border: 1px solid #c9c9c9;
      border-radius: 4px;
      background: #fff;
      cursor: pointer;
    }
    .preview-card button.save {
      background: #4682b4;
      border-color: #4682b4;
      color: #fff;
    }
    .preview-card button.save:hover { background: #3a6f99; }
    .preview-card button.cancel:hover { background: #f5f5f5; }
`;

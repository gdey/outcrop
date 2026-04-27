// Typed message envelopes exchanged between popup, content script, and
// background service worker. All messages are sent via runtime.sendMessage or
// tabs.sendMessage as one-shot (no port).

export type Rect = {
  x: number;
  y: number;
  w: number;
  h: number;
  dpr: number;
};

// popup → background: user clicked Capture in the popup.
export type BeginMessage = {
  type: "begin";
  vaultKey: string;
  vaultName: string;
};

// content → background: user finished dragging the rectangle.
export type RectMessage = {
  type: "rect";
  rect: Rect;
  url: string;
  title: string;
  selectedText: string;
};

// background → content (via tabs.sendMessage): cropped image ready to preview.
export type CroppedMessage = {
  type: "cropped";
  imageBase64: string;
  vaultName: string;
};

// content → background: user clicked Save in the preview card.
export type SaveMessage = {
  type: "save";
  notes: string;
};

// content → background: user pressed Escape (in either phase).
export type CancelMessage = {
  type: "cancel";
};

export type Message =
  | BeginMessage
  | RectMessage
  | CroppedMessage
  | SaveMessage
  | CancelMessage;

// background.ts — the service worker. Owns:
//   - the capture state machine, persisted in storage.session per tab so a SW
//     restart during the human-paced notes window survives;
//   - tabs.captureVisibleTab + OffscreenCanvas crop;
//   - POST /clip and the success/failure notification.
//
// The popup and content script never talk to the server directly.

import { ApiError, Client } from "./lib/api";
import { cropToBase64 } from "./lib/crop";
import type {
  CancelMessage,
  CroppedMessage,
  Message,
  RectMessage,
  SaveMessage,
} from "./lib/messages";
import { isConfigured, loadSettings } from "./lib/settings";

type CaptureState = {
  vaultKey: string;
  vaultName: string;
  windowId: number;
  url: string;
  title: string;
  selectedText: string;
  imageBase64: string;
};

const STATE_PREFIX = "capture_";

function stateKey(tabId: number): string {
  return STATE_PREFIX + tabId;
}

async function getState(tabId: number): Promise<CaptureState | undefined> {
  const r = await browser.storage.session.get(stateKey(tabId));
  return r[stateKey(tabId)] as CaptureState | undefined;
}

async function setState(tabId: number, s: CaptureState): Promise<void> {
  await browser.storage.session.set({ [stateKey(tabId)]: s });
}

async function clearState(tabId: number): Promise<void> {
  await browser.storage.session.remove(stateKey(tabId));
}

async function notify(title: string, message: string): Promise<void> {
  await browser.notifications.create({
    type: "basic",
    title,
    message,
    iconUrl: browser.runtime.getURL("icons/48.png"),
  });
}

browser.runtime.onMessage.addListener((msg: Message, sender) => {
  switch (msg.type) {
    case "begin":
      return handleBegin(msg.vaultKey, msg.vaultName);
    case "rect":
      return handleRect(msg, sender);
    case "save":
      return handleSave(msg, sender);
    case "cancel":
      return handleCancel(sender);
    case "cropped":
      // Background never receives "cropped" — it sends them. Ignore.
      return undefined;
    default:
      return undefined;
  }
});

async function handleBegin(vaultKey: string, vaultName: string): Promise<void> {
  const tabs = await browser.tabs.query({ active: true, currentWindow: true });
  const tab = tabs[0];
  if (!tab?.id || tab.windowId === undefined) return;

  await setState(tab.id, {
    vaultKey,
    vaultName,
    windowId: tab.windowId,
    url: "",
    title: "",
    selectedText: "",
    imageBase64: "",
  });

  try {
    await browser.scripting.executeScript({
      target: { tabId: tab.id },
      files: ["content.js"],
    });
  } catch (err) {
    await clearState(tab.id);
    await notify("Outcrop: cannot inject", (err as Error).message);
  }
}

async function handleRect(msg: RectMessage, sender: browser.runtime.MessageSender): Promise<void> {
  const tabId = sender.tab?.id;
  if (tabId === undefined) return;
  const state = await getState(tabId);
  if (!state) return;

  let dataURL: string;
  try {
    dataURL = await browser.tabs.captureVisibleTab(state.windowId, { format: "png" });
  } catch (err) {
    await clearState(tabId);
    await notify("Outcrop: capture failed", (err as Error).message);
    return;
  }

  let imageBase64: string;
  try {
    imageBase64 = await cropToBase64(dataURL, msg.rect);
  } catch (err) {
    await clearState(tabId);
    await notify("Outcrop: crop failed", (err as Error).message);
    return;
  }

  state.url = msg.url;
  state.title = msg.title;
  state.selectedText = msg.selectedText;
  state.imageBase64 = imageBase64;
  await setState(tabId, state);

  const reply: CroppedMessage = {
    type: "cropped",
    imageBase64,
    vaultName: state.vaultName,
  };
  try {
    await browser.tabs.sendMessage(tabId, reply);
  } catch (err) {
    console.error("[outcrop] tabs.sendMessage(cropped) failed", err);
    await clearState(tabId);
  }
}

async function handleSave(msg: SaveMessage, sender: browser.runtime.MessageSender): Promise<void> {
  const tabId = sender.tab?.id;
  if (tabId === undefined) return;
  const state = await getState(tabId);
  if (!state) return;
  await clearState(tabId);

  const settings = await loadSettings();
  if (!isConfigured(settings)) {
    await notify("Outcrop: not configured", "Open the options page to set the server URL and token.");
    return;
  }

  const client = new Client(settings.serverURL, settings.token);
  try {
    await client.clip({
      vault: state.vaultKey,
      url: state.url,
      title: state.title,
      selectedText: state.selectedText,
      notes: msg.notes,
      imageBase64: state.imageBase64,
    });
    await notify("Outcrop", `Saved to ${state.vaultName}.`);
  } catch (err) {
    let detail = (err as Error).message;
    if (err instanceof ApiError) detail = `${err.code} — ${err.message}`;
    console.error("[outcrop] POST /clip failed", err);
    await notify("Outcrop: save failed", detail);
  }
}

async function handleCancel(sender: browser.runtime.MessageSender): Promise<void> {
  const tabId = sender.tab?.id;
  if (tabId === undefined) return;
  await clearState(tabId);
}

// Toolbar badge: show "!" until the extension is configured.
async function refreshBadge(): Promise<void> {
  const s = await loadSettings();
  if (isConfigured(s)) {
    await browser.action.setBadgeText({ text: "" });
  } else {
    await browser.action.setBadgeText({ text: "!" });
    await browser.action.setBadgeBackgroundColor({ color: "#b22222" });
  }
}

browser.runtime.onInstalled.addListener((details) => {
  refreshBadge().catch(() => {});
  if (details.reason === "install") {
    browser.runtime.openOptionsPage().catch(() => {});
  }
});

browser.runtime.onStartup.addListener(() => {
  refreshBadge().catch(() => {});
});

browser.storage.onChanged.addListener((_changes, area) => {
  if (area === "local") {
    refreshBadge().catch(() => {});
  }
});

// Drop in-flight state when a tab is closed or navigates away.
browser.tabs.onRemoved.addListener((tabId) => {
  clearState(tabId).catch(() => {});
});

browser.tabs.onUpdated.addListener((tabId, changeInfo) => {
  if (changeInfo.url) clearState(tabId).catch(() => {});
});

// First wake of the SW after install/load: ensure the badge reflects state.
refreshBadge().catch(() => {});

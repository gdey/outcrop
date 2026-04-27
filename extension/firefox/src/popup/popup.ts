import { ApiError, Client, type Vault } from "../lib/api";
import { clear, el } from "../lib/dom";
import type { BeginMessage } from "../lib/messages";
import { isConfigured, loadSettings, type Settings } from "../lib/settings";

const root = document.getElementById("root");
if (!root) throw new Error("popup root missing");

async function init(): Promise<void> {
  const settings = await loadSettings();
  if (!isConfigured(settings)) {
    renderNotConfigured();
    return;
  }
  const tab = await getActiveTab();
  await renderReady(settings, tab);
}

async function getActiveTab(): Promise<browser.tabs.Tab | undefined> {
  const tabs = await browser.tabs.query({ active: true, currentWindow: true });
  return tabs[0];
}

function settingsLink(): HTMLAnchorElement {
  const a = el("a", { href: "#", className: "settings-link" }, "Open Settings →");
  a.addEventListener("click", (e) => {
    e.preventDefault();
    browser.runtime.openOptionsPage();
    window.close();
  });
  return a;
}

function errorBox(message: string): HTMLDivElement {
  return el(
    "div",
    { className: "error" },
    message,
    el("div", { className: "error-actions" }, settingsLink()),
  );
}

function renderNotConfigured(): void {
  if (!root) return;
  clear(root);
  root.appendChild(errorBox("Outcrop is not configured."));
}

function renderUnreachable(err: unknown): void {
  if (!root) return;
  let msg = "Server is unreachable.";
  if (err instanceof ApiError) {
    msg = `Server error: ${err.code} — ${err.message}`;
  } else if (err instanceof Error) {
    msg = `Cannot reach server: ${err.message}`;
  }
  clear(root);
  root.appendChild(errorBox(msg));
}

function renderEmptyVaults(): void {
  if (!root) return;
  clear(root);
  root.appendChild(
    el(
      "div",
      { className: "error" },
      "No vaults configured. Run ",
      el("code", {}, "outcrop vault add"),
      " on the server side.",
    ),
  );
}

async function renderReady(settings: Settings, tab: browser.tabs.Tab | undefined): Promise<void> {
  if (!root) return;
  const client = new Client(settings.serverURL, settings.token);

  try {
    await client.health();
  } catch (err) {
    renderUnreachable(err);
    return;
  }

  let vaults: Vault[];
  try {
    vaults = await client.vaults(tab?.url, tab?.title);
  } catch (err) {
    renderUnreachable(err);
    return;
  }

  if (vaults.length === 0) {
    renderEmptyVaults();
    return;
  }

  let selected = vaults[0]!;
  let expanded = false;

  function render(): void {
    if (!root) return;
    clear(root);

    const pill = el(
      "button",
      {
        className: "vault-pill",
        type: "button",
        disabled: vaults.length === 1,
      },
      el(
        "span",
        {},
        selected.displayName,
        selected.isDefault ? el("span", { className: "star" }, "★") : null,
      ),
      vaults.length > 1 ? el("span", { className: "caret" }, "▾") : null,
    );

    const list = el("ul", { className: "vault-list" });
    list.style.display = expanded ? "block" : "none";
    for (const v of vaults) {
      const li = el(
        "li",
        { className: v.key === selected.key ? "selected" : "" },
        v.displayName,
        v.isDefault ? el("span", { className: "star" }, "★") : null,
      );
      li.addEventListener("click", () => {
        selected = v;
        expanded = false;
        render();
      });
      list.appendChild(li);
    }

    if (vaults.length > 1) {
      pill.addEventListener("click", () => {
        expanded = !expanded;
        list.style.display = expanded ? "block" : "none";
      });
    }

    const captureBtn = el("button", { className: "primary", type: "button" }, "Capture");
    captureBtn.addEventListener("click", onCapture);

    root.appendChild(
      el(
        "div",
        { className: "vault-section" },
        el("div", { className: "label" }, "Save to"),
        pill,
        list,
      ),
    );
    root.appendChild(el("div", { className: "actions" }, captureBtn));
    root.appendChild(settingsLink());
  }

  async function onCapture(): Promise<void> {
    const msg: BeginMessage = {
      type: "begin",
      vaultKey: selected.key,
      vaultName: selected.displayName,
    };
    await browser.runtime.sendMessage(msg);
    window.close();
  }

  render();
}

init().catch((e) => {
  if (!root) return;
  clear(root);
  root.appendChild(errorBox(`Init failed: ${(e as Error).message}`));
});

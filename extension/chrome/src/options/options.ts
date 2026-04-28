import { ApiError, Client } from "../lib/api";
import { $ } from "../lib/dom";
import { loadSettings, saveSettings } from "../lib/settings";

const form = $<HTMLFormElement>("form");
const serverEl = $<HTMLInputElement>("serverURL");
const tokenEl = $<HTMLInputElement>("token");
const testBtn = $<HTMLButtonElement>("test");
const statusEl = $<HTMLParagraphElement>("status");

function setStatus(text: string, kind: "ok" | "err" | "" = ""): void {
  statusEl.textContent = text;
  statusEl.classList.toggle("ok", kind === "ok");
  statusEl.classList.toggle("err", kind === "err");
}

async function init(): Promise<void> {
  const s = await loadSettings();
  serverEl.value = s.serverURL;
  tokenEl.value = s.token;
}

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  await saveSettings({
    serverURL: serverEl.value.trim(),
    token: tokenEl.value,
  });
  setStatus("Saved.", "ok");
});

testBtn.addEventListener("click", async () => {
  setStatus("Testing…");
  testBtn.disabled = true;
  try {
    const c = new Client(serverEl.value.trim(), tokenEl.value);
    await c.health();
    setStatus("Connected. Server is reachable and the token works.", "ok");
  } catch (err) {
    if (err instanceof ApiError) {
      setStatus(`Failed: ${err.code} — ${err.message}`, "err");
    } else {
      setStatus(`Failed: ${(err as Error).message}`, "err");
    }
  } finally {
    testBtn.disabled = false;
  }
});

init().catch((e) => setStatus(`Init failed: ${(e as Error).message}`, "err"));

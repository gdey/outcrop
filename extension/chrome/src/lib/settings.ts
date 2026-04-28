const KEY_SERVER_URL = "serverURL";
const KEY_TOKEN = "token";
const DEFAULT_SERVER_URL = "http://127.0.0.1:7878";

export type Settings = {
  serverURL: string;
  token: string;
};

export async function loadSettings(): Promise<Settings> {
  const r = await chrome.storage.local.get([KEY_SERVER_URL, KEY_TOKEN]);
  return {
    serverURL: typeof r[KEY_SERVER_URL] === "string" ? r[KEY_SERVER_URL] : DEFAULT_SERVER_URL,
    token: typeof r[KEY_TOKEN] === "string" ? r[KEY_TOKEN] : "",
  };
}

export async function saveSettings(s: Settings): Promise<void> {
  await chrome.storage.local.set({
    [KEY_SERVER_URL]: s.serverURL,
    [KEY_TOKEN]: s.token,
  });
}

export function isConfigured(s: Settings): boolean {
  return s.token !== "" && s.serverURL !== "";
}

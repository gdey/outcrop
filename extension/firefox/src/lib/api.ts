// Typed client for the outcrop server. Mirrors the wire shapes defined in
// docs/rfd/0003-v1-server.md.

export type Vault = {
  key: string;
  displayName: string;
  isDefault: boolean;
};

export type ClipRequest = {
  vault: string;
  url: string;
  title: string;
  selectedText: string;
  notes: string;
  imageBase64: string;
};

export type ClipResponse = {
  notePath: string;
  imagePath: string;
};

export type ApiErrorBody = {
  error: string;
  message: string;
};

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export class Client {
  private baseURL: string;
  private token: string;

  constructor(baseURL: string, token: string) {
    this.baseURL = baseURL.replace(/\/+$/, "");
    this.token = token;
  }

  async health(): Promise<void> {
    await this.request<{ status: string }>("GET", "/healthz");
  }

  async vaults(url?: string, title?: string): Promise<Vault[]> {
    const params = new URLSearchParams();
    if (url) params.set("url", url);
    if (title) params.set("title", title);
    const q = params.toString();
    return this.request<Vault[]>("GET", q ? `/vaults?${q}` : "/vaults");
  }

  async clip(req: ClipRequest): Promise<ClipResponse> {
    return this.request<ClipResponse>("POST", "/clip", req);
  }

  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const headers = new Headers();
    headers.set("Authorization", `Bearer ${this.token}`);
    if (body !== undefined) headers.set("Content-Type", "application/json");

    const res = await fetch(this.baseURL + path, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });

    const text = await res.text();
    if (!res.ok) {
      let code = `http_${res.status}`;
      let message = text || res.statusText;
      if (text) {
        try {
          const parsed = JSON.parse(text) as Partial<ApiErrorBody>;
          if (typeof parsed.error === "string") code = parsed.error;
          if (typeof parsed.message === "string") message = parsed.message;
        } catch {
          /* fall through with raw text */
        }
      }
      throw new ApiError(res.status, code, message);
    }
    return text ? (JSON.parse(text) as T) : (undefined as T);
  }
}

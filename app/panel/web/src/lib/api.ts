let csrf = "";
let csrfWaiters: Array<() => void> = [];

export function setCsrf(token: string) {
  csrf = token;
  if (!csrf) return;
  const waiters = csrfWaiters;
  csrfWaiters = [];
  for (const wait of waiters) wait();
}

function waitForCsrf(): Promise<void> {
  if (csrf) return Promise.resolve();
  return new Promise((resolve) => {
    csrfWaiters.push(resolve);
  });
}

function mutationNeedsCsrf(path: string, init: RequestInit): boolean {
  const method = (init.method ?? "GET").toUpperCase();
  if (method === "GET" || method === "HEAD") return false;
  return path !== "/api/login";
}

export async function apiRequest(path: string, init: RequestInit = {}): Promise<Response> {
  if (mutationNeedsCsrf(path, init)) {
    await waitForCsrf();
  }
  const headers = new Headers(init.headers);
  if (csrf) headers.set("X-CSRF-Token", csrf);
  if (init.body && !(init.body instanceof FormData)) {
    headers.set("Content-Type", "application/json");
  }
  const res = await fetch(path, { ...init, headers, credentials: "same-origin" });
  if (res.status === 401 && path !== "/api/login") {
    window.location.assign("/login");
    throw new Error("Unauthorized.");
  }
  return res;
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await apiRequest(path, init);
  let data: T | undefined;
  try {
    data = (await res.json()) as T;
  } catch {
    data = undefined;
  }
  return data as T;
}

export type LoginResponse = {
  ok: boolean;
  need_code?: boolean;
  message?: string;
};

export type MeResponse = {
  username: string;
  csrf: string;
  totp: { enabled: boolean };
};

export type Client = {
  id: number;
  name: string;
  description: string;
  address: string;
  enabled: boolean;
  online: boolean;
  last_handshake_utc: string | null;
  rx_bytes: number;
  tx_bytes: number;
};

export type MutationResponse = {
  ok?: boolean;
  message?: string;
  id?: number;
};

/** Toast only after a confirmed success: `{ok:true}` or a created/patched client. */
export function mutationSucceeded(data: MutationResponse | undefined): boolean {
  if (!data) return false;
  if (data.ok === true) return true;
  if (data.ok === false) return false;
  return typeof data.id === "number";
}

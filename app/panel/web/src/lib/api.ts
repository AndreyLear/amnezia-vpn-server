import { toast } from "sonner";

let csrf = "";
let lastUsername = "";
let csrfWaiters: Array<() => void> = [];
let sessionExpiredWaiters: Array<() => void> = [];
let sessionExpiredListeners: Array<(open: boolean) => void> = [];

export function setCsrf(token: string) {
  csrf = token;
  if (!csrf) return;
  const waiters = csrfWaiters;
  csrfWaiters = [];
  for (const wait of waiters) wait();
}

export function setLastUsername(username: string) {
  lastUsername = username;
}

export function getLastUsername(): string {
  return lastUsername;
}

export function subscribeSessionExpired(listener: (open: boolean) => void): () => void {
  sessionExpiredListeners.push(listener);
  return () => {
    sessionExpiredListeners = sessionExpiredListeners.filter((item) => item !== listener);
  };
}

export function completeSessionRelogin() {
  const waiters = sessionExpiredWaiters;
  sessionExpiredWaiters = [];
  for (const wait of waiters) wait();
  for (const listener of sessionExpiredListeners) listener(false);
}

function waitForCsrf(): Promise<void> {
  if (csrf) return Promise.resolve();
  return new Promise((resolve) => {
    const timer = window.setTimeout(() => {
      csrfWaiters = csrfWaiters.filter((w) => w !== done);
      resolve();
    }, 50);
    const done = () => {
      window.clearTimeout(timer);
      resolve();
    };
    csrfWaiters.push(done);
  });
}

function waitForSessionRelogin(): Promise<void> {
  return new Promise((resolve) => {
    sessionExpiredWaiters.push(resolve);
    for (const listener of sessionExpiredListeners) listener(true);
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
    if (!csrf) {
      window.location.assign("/login");
      throw new Error("Unauthorized.");
    }
    await waitForSessionRelogin();
    return apiRequest(path, init);
  }
  return res;
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await apiRequest(path, init);
  if (res.status === 403 && path !== "/api/me" && path !== "/api/login") {
    try {
      const meRes = await fetch("/api/me", { credentials: "same-origin" });
      if (meRes.ok) {
        const me = (await meRes.json()) as MeResponse;
        if (me.csrf) setCsrf(me.csrf);
      }
    } catch {
      // keep going so the original JSON body can still be parsed
    }
    toast.error("сессия устарела");
  }
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
  restore_pending?: boolean;
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

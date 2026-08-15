let csrf = "";

export function setCsrf(token: string) {
  csrf = token;
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (csrf) headers.set("X-CSRF-Token", csrf);
  if (init.body && !(init.body instanceof FormData)) {
    headers.set("Content-Type", "application/json");
  }
  const res = await fetch(path, { ...init, headers, credentials: "same-origin" });
  let data: T | undefined;
  try {
    data = (await res.json()) as T;
  } catch {
    data = undefined;
  }
  if (res.status === 401 && path !== "/api/login") {
    window.location.assign("/login");
    throw new Error("Unauthorized.");
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

import { afterEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";

import { api, completeSessionRelogin, resetSessionStateForTests, setCsrf } from "@/lib/api";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("api CSRF", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.clearAllMocks();
    setCsrf("");
  });

  it("does not POST a mutation until CSRF is set, then sends X-CSRF-Token", async () => {
    const fetchMock = vi.fn(async () => jsonResponse({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    const pending = api("/api/clients", {
      method: "POST",
      body: JSON.stringify({ name: "phone", description: "" }),
    });

    await Promise.resolve();
    expect(fetchMock).not.toHaveBeenCalled();

    setCsrf("secret-csrf");
    await pending;

    expect(fetchMock).toHaveBeenCalledOnce();
    const call = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    const headers = new Headers(call[1].headers);
    expect(headers.get("X-CSRF-Token")).toBe("secret-csrf");
  });

  it("does not wait for CSRF on POST /api/login", async () => {
    const fetchMock = vi.fn(async () => jsonResponse({ ok: false }));
    vi.stubGlobal("fetch", fetchMock);

    await api("/api/login", {
      method: "POST",
      body: JSON.stringify({ username: "a", password: "b" }),
    });

    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it("refreshes CSRF from GET /api/me on 403 and does not hang when csrf is empty", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.includes("/api/clients") && !path.includes("/api/me")) {
        return jsonResponse({ ok: false, message: "Forbidden." }, 403);
      }
      if (path.includes("/api/me")) {
        return jsonResponse({ csrf: "fresh-token" });
      }
      throw new Error(path);
    });
    vi.stubGlobal("fetch", fetchMock);

    const started = Date.now();
    const data = await api<{ ok?: boolean; message?: string }>("/api/clients", {
      method: "POST",
      body: JSON.stringify({ name: "x" }),
    });
    expect(Date.now() - started).toBeLessThan(1000);
    expect(data.message).toBe("Forbidden.");
    expect(toast.error).toHaveBeenCalledWith("Сессия устарела");
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/me",
      expect.objectContaining({ credentials: "same-origin" }),
    );
  });

  // Медленный /api/me: мутация ушла без токена, сервер ответил 403. Токен
  // пришёл — действие повторяется один раз, а не теряется с тостом
  // «Сессия устарела» (amnezia-vpn-server-76mp.37).
  it("retries a mutation once with the fresh token after 403 without CSRF", async () => {
    const posts: Array<string | null> = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/me") return jsonResponse({ csrf: "late-token" });
      if (path === "/api/mail") {
        const token = new Headers(init?.headers).get("X-CSRF-Token");
        posts.push(token);
        if (token !== "late-token") return jsonResponse({ ok: false, message: "Forbidden." }, 403);
        return jsonResponse({ ok: true });
      }
      throw new Error(path);
    });
    vi.stubGlobal("fetch", fetchMock);

    const data = await api<{ ok?: boolean }>("/api/mail", {
      method: "PUT",
      body: JSON.stringify({ host: "smtp" }),
    });

    expect(data.ok).toBe(true);
    expect(posts).toEqual([null, "late-token"]);
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("does not retry a 403 mutation when the token did not change", async () => {
    setCsrf("same-token");
    let puts = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        if (path === "/api/me") return jsonResponse({ csrf: "same-token" });
        puts += 1;
        return jsonResponse({ ok: false, message: "Forbidden." }, 403);
      }),
    );

    await api("/api/mail", { method: "PUT", body: "{}" });

    expect(puts).toBe(1);
    expect(toast.error).toHaveBeenCalledWith("Сессия устарела");
  });
});

// Вкладка в фоне на ночь: сессия истекла, а опрос раз в 5 с продолжал
// ставить в очередь новых ожидающих — после входа они уходили залпом
// (amnezia-vpn-server-76mp.7).
describe("api session loss", () => {
  afterEach(() => {
    resetSessionStateForTests();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    setCsrf("");
  });

  it("keeps at most one pending request per path while the session is lost", async () => {
    setCsrf("live-csrf");
    let expired = true;
    const calls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        calls.push(path);
        if (expired) return jsonResponse({ ok: false, reason: "idle" }, 401);
        return jsonResponse([{ id: 1 }]);
      }),
    );

    const first = api<unknown[]>("/api/clients");
    await new Promise((r) => setTimeout(r, 0));
    const ticks = Array.from({ length: 100 }, () => [
      api<unknown[]>("/api/clients"),
      api<unknown>("/api/stats/host"),
    ]).flat();
    await new Promise((r) => setTimeout(r, 0));

    // Пока сессии нет, сервер не получает запрос на каждый тик.
    expect(calls.filter((p) => p === "/api/clients")).toHaveLength(1);
    expect(calls.filter((p) => p === "/api/stats/host").length).toBeLessThanOrEqual(1);

    expired = false;
    calls.length = 0;
    completeSessionRelogin();
    const results = await Promise.all([first, ...ticks]);

    expect(calls.filter((p) => p === "/api/clients").length).toBeLessThanOrEqual(1);
    expect(calls.filter((p) => p === "/api/stats/host").length).toBeLessThanOrEqual(1);
    // Каждый ждавший всё равно получил ответ, а не пустое тело.
    expect(results[0]).toEqual([{ id: 1 }]);
    expect(results.at(-1)).toEqual([{ id: 1 }]);
  });
});

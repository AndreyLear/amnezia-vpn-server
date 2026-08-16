import { afterEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";

import { api, setCsrf } from "@/lib/api";

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
        return jsonResponse({ csrf: "fresh-token", totp: { enabled: false } });
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
    expect(toast.error).toHaveBeenCalledWith("сессия устарела");
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/me",
      expect.objectContaining({ credentials: "same-origin" }),
    );
  });
});

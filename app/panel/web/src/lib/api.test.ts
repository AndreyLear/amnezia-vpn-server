import { afterEach, describe, expect, it, vi } from "vitest";

import { api, setCsrf } from "@/lib/api";

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
});

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { SessionExpiredHost } from "@/components/SessionExpiredHost";
import { apiRequest, setCsrf, setLastUsername } from "@/lib/api";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("session expired re-login", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    setCsrf("");
    setLastUsername("");
  });

  it("opens re-login dialog on 401 when csrf is set and does not redirect", async () => {
    setCsrf("live-csrf");
    setLastUsername("admin");
    const assign = vi.fn();
    vi.stubGlobal("location", { assign });
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse({ ok: false, message: "Unauthorized." }, 401)),
    );

    render(<SessionExpiredHost />);
    void apiRequest("/api/clients");

    expect(await screen.findByText("Нужно войти заново")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Повторить вход" })).toBeInTheDocument();
    expect(assign).not.toHaveBeenCalled();
  });

  it("redirects to /login on 401 when csrf was never set", async () => {
    const assign = vi.fn();
    vi.stubGlobal("location", { assign });
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse({ ok: false, message: "Unauthorized." }, 401)),
    );

    render(<SessionExpiredHost />);
    await expect(apiRequest("/api/clients")).rejects.toThrow("Unauthorized.");
    expect(assign).toHaveBeenCalledWith("/login");
    expect(screen.queryByText("Нужно войти заново")).not.toBeInTheDocument();
  });

  it("retries the failed request after successful Повторить вход", async () => {
    setCsrf("live-csrf");
    setLastUsername("admin");
    const assign = vi.fn();
    vi.stubGlobal("location", { assign });
    let clientCalls = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/clients") {
        clientCalls += 1;
        if (clientCalls === 1) {
          return jsonResponse({ ok: false, message: "Unauthorized." }, 401);
        }
        return jsonResponse({ ok: true });
      }
      if (path === "/api/login") {
        expect(init?.method).toBe("POST");
        return jsonResponse({ ok: true });
      }
      if (path === "/api/me") {
        return jsonResponse({
          username: "admin",
          csrf: "fresh-csrf",
          totp: { enabled: false },
        });
      }
      throw new Error(path);
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<SessionExpiredHost />);
    const pending = apiRequest("/api/clients");
    expect(await screen.findByText("Нужно войти заново")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Пароль"), "secret");
    await user.click(screen.getByRole("button", { name: "Повторить вход" }));

    const res = await pending;
    expect(res.ok).toBe(true);
    expect(assign).not.toHaveBeenCalled();
    expect(clientCalls).toBeGreaterThanOrEqual(2);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/login",
      expect.objectContaining({ method: "POST" }),
    );
  });
});

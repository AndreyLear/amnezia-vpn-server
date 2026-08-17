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

async function openWithReason(reason: "idle" | "replaced" | "gone") {
  setCsrf("live-csrf");
  setLastUsername("admin");
  const assign = vi.fn();
  vi.stubGlobal("location", { assign });
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      jsonResponse({ ok: false, message: "Unauthorized.", reason }, 401),
    ),
  );
  render(<SessionExpiredHost />);
  void apiRequest("/api/clients");
  return assign;
}

describe("session expired re-login", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    setCsrf("");
    setLastUsername("");
  });

  it("opens idle dialog on 401 when csrf is set and does not redirect", async () => {
    const assign = await openWithReason("idle");

    expect(await screen.findByText("Сессия истекла")).toBeInTheDocument();
    expect(screen.getByText("Введите пароль, чтобы продолжить")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Повторить вход" })).toHaveClass("max-sm:h-12", "max-sm:w-full");
    const content = document.querySelector("[data-slot=dialog-content]");
    expect(content).toHaveClass("gap-6");
    const form = content?.querySelector("form");
    expect(form).toHaveClass("grid", "gap-6");
    expect(form).not.toHaveClass("gap-3");
    expect(screen.queryByText("Нужно войти заново")).not.toBeInTheDocument();
    expect(assign).not.toHaveBeenCalled();
  });

  it("shows replaced title and description", async () => {
    await openWithReason("replaced");

    expect(await screen.findByText("Вход с другого устройства")).toBeInTheDocument();
    expect(
      screen.getByText("Эта сессия закрыта. Если это были не вы — смените пароль через CLI"),
    ).toBeInTheDocument();
  });

  it("shows gone title and description", async () => {
    await openWithReason("gone");

    expect(await screen.findByText("Сессия сброшена")).toBeInTheDocument();
    expect(
      screen.getByText(
        "Панель перезапустили или пароль сменили на сервере. Введите пароль, чтобы продолжить",
      ),
    ).toBeInTheDocument();
  });

  it("does not clear the typed password on a second session-expired signal", async () => {
    setCsrf("live-csrf");
    setLastUsername("admin");
    vi.stubGlobal("location", { assign: vi.fn() });
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse({ ok: false, message: "Unauthorized.", reason: "idle" }, 401),
      ),
    );

    render(<SessionExpiredHost />);
    void apiRequest("/api/clients");
    expect(await screen.findByText("Сессия истекла")).toBeInTheDocument();

    const user = userEvent.setup();
    const password = screen.getByLabelText("Пароль");
    await user.type(password, "draft-secret");
    expect(password).toHaveValue("draft-secret");

    void apiRequest("/api/clients");
    await screen.findByDisplayValue("draft-secret");
    expect(screen.getByLabelText("Пароль")).toHaveValue("draft-secret");
  });

  it("treats empty password as unfilled and does not show имя пользователя", async () => {
    setCsrf("live-csrf");
    setLastUsername("admin");
    vi.stubGlobal("location", { assign: vi.fn() });
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === "/api/clients") {
        return jsonResponse({ ok: false, message: "Unauthorized.", reason: "idle" }, 401);
      }
      if (path === "/api/login") {
        return jsonResponse({
          ok: false,
          message: "Неверное имя пользователя или пароль.",
        });
      }
      throw new Error(path);
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<SessionExpiredHost />);
    void apiRequest("/api/clients");
    expect(await screen.findByText("Сессия истекла")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Повторить вход" }));

    expect(screen.queryByText(/имя пользователя/i)).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith(
      "/api/login",
      expect.anything(),
    );
  });

  it("maps login-style API error so the modal never mentions username", async () => {
    setCsrf("live-csrf");
    setLastUsername("admin");
    vi.stubGlobal("location", { assign: vi.fn() });
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === "/api/clients") {
        return jsonResponse({ ok: false, message: "Unauthorized.", reason: "idle" }, 401);
      }
      if (path === "/api/login") {
        return jsonResponse({
          ok: false,
          message: "Неверное имя пользователя или пароль.",
        });
      }
      throw new Error(path);
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<SessionExpiredHost />);
    void apiRequest("/api/clients");
    expect(await screen.findByText("Сессия истекла")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Пароль"), "wrong");
    await user.click(screen.getByRole("button", { name: "Повторить вход" }));

    expect(await screen.findByText("Неверный пароль")).toBeInTheDocument();
    expect(screen.queryByText(/имя пользователя/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/логин/i)).not.toBeInTheDocument();
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
    expect(screen.queryByText("Сессия истекла")).not.toBeInTheDocument();
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
          return jsonResponse({ ok: false, message: "Unauthorized.", reason: "idle" }, 401);
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
        });
      }
      throw new Error(path);
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<SessionExpiredHost />);
    const pending = apiRequest("/api/clients");
    expect(await screen.findByText("Сессия истекла")).toBeInTheDocument();

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

import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { setCsrf, type Client } from "@/lib/api";
import HomePage from "@/pages/HomePage";

const alice: Client = {
  id: 1,
  name: "Alice",
  description: "",
  address: "10.8.0.2/32",
  enabled: true,
  online: true,
  last_handshake_utc: "2026-08-16T00:00:00Z",
  rx_bytes: 0,
  tx_bytes: 0,
};

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("HomePage load", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    setCsrf("");
  });

  it("keeps previous clients when GET /api/clients is not a successful array", async () => {
    vi.useFakeTimers({ toFake: ["setInterval"] });
    let clientCalls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        if (path.includes("/api/me")) {
          return jsonResponse({
            username: "admin",
            csrf: "token",
            totp: { enabled: false },
          });
        }
        if (path.includes("/api/clients")) {
          clientCalls += 1;
          if (clientCalls === 1) return jsonResponse([alice]);
          return jsonResponse({ ok: false }, 500);
        }
        throw new Error(path);
      }),
    );

    render(<HomePage />);
    expect(await screen.findByText("Alice")).toBeInTheDocument();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });

    expect(clientCalls).toBeGreaterThanOrEqual(2);
    expect(screen.getByText("Alice")).toBeInTheDocument();
  });

  it("lists clients in one column with mt-6 gap-2 and no add tile", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        if (path.includes("/api/me")) {
          return jsonResponse({
            username: "admin",
            csrf: "token",
            totp: { enabled: false },
          });
        }
        if (path.includes("/api/clients")) {
          return jsonResponse([alice]);
        }
        throw new Error(path);
      }),
    );

    render(<HomePage />);
    expect(await screen.findByText("Alice")).toBeInTheDocument();

    const grid = screen.getByTestId("client-grid");
    expect(grid).toHaveClass("mt-6", "flex", "flex-col", "gap-2", "pb-8");
    expect(grid.className).not.toMatch(/min-\[752px\]:grid-cols-2/);
    expect(within(grid).queryByRole("button", { name: /добавить клиента/i })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Добавить клиента" })).toBeInTheDocument();
  });

  it("flips enabled locally after a successful toggle even if GET stays stale", async () => {
    const user = userEvent.setup();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        const method = (init?.method ?? "GET").toUpperCase();
        if (path.includes("/api/me")) {
          return jsonResponse({
            username: "admin",
            csrf: "token",
            totp: { enabled: false },
          });
        }
        if (path.includes("/api/clients/1") && method === "PATCH") {
          return jsonResponse({ ok: true });
        }
        if (path.includes("/api/clients")) {
          return jsonResponse([alice]);
        }
        throw new Error(path);
      }),
    );

    render(<HomePage />);
    expect(await screen.findByText("Alice")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Действия для Alice" }));
    await user.click(screen.getByRole("menuitem", { name: "Отключить" }));

    expect(await screen.findByText("Пауза")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Действия для Alice" }));
    expect(screen.getByRole("menuitem", { name: "Включить" })).toBeInTheDocument();
  });
});

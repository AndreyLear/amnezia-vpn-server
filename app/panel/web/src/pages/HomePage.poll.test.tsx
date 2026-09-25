import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { setCsrf, type Client } from "@/lib/api";
import HomePage from "@/pages/HomePage";

const alice: Client = {
  id: 1,
  name: "Alice",
  description: "",
  address: "10.8.0.2/32",
  address6: "",
  enabled: true,
  online: true,
  last_handshake_utc: "2026-08-16T00:00:00Z",
  rx_bytes: 0,
  tx_bytes: 0,
  mtu: 0,
  rate_limit: 0,
  dns_bypass: false,
};

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

let visibility: DocumentVisibilityState = "visible";

function setVisibility(next: DocumentVisibilityState) {
  visibility = next;
  document.dispatchEvent(new Event("visibilitychange"));
}

describe("HomePage polling", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    setCsrf("");
    visibility = "visible";
  });

  // Вкладка в фоне не опрашивает сервер: за ночь опрос копил тысячи
  // запросов, ждущих повторного входа (amnezia-vpn-server-76mp.7).
  it("pauses the poll while the tab is hidden and reloads on return", async () => {
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      get: () => visibility,
    });
    vi.useFakeTimers({ toFake: ["setInterval"] });
    let clientCalls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        if (path.includes("/api/me")) return jsonResponse({ username: "admin", csrf: "token" });
        if (path.includes("/api/clients")) {
          clientCalls += 1;
          return jsonResponse([alice]);
        }
        if (path.includes("/api/stats/host")) return jsonResponse({});
        return jsonResponse({}, 404);
      }),
    );

    render(<HomePage />);
    expect(await screen.findByText("Alice")).toBeInTheDocument();
    const afterBoot = clientCalls;

    act(() => setVisibility("hidden"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000 * 10);
    });
    expect(clientCalls).toBe(afterBoot);

    await act(async () => {
      setVisibility("visible");
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(clientCalls).toBe(afterBoot + 1);
  });

  // Опрос, ушедший до PATCH, вернулся после него и вернул карточке прежнее
  // состояние при тосте «Клиент отключён» (amnezia-vpn-server-76mp.17).
  it("drops a poll answer that is older than the list loaded after a toggle", async () => {
    vi.useFakeTimers({ toFake: ["setInterval"] });
    let clientCalls = 0;
    let serverEnabled = true;
    let releaseStalePoll: (() => void) | null = null;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        if (path.includes("/api/me")) return jsonResponse({ username: "admin", csrf: "token" });
        if (path === "/api/clients/1" && init?.method === "PATCH") {
          serverEnabled = false;
          return jsonResponse({ ok: true });
        }
        if (path === "/api/clients") {
          clientCalls += 1;
          const snapshot = [{ ...alice, enabled: serverEnabled }];
          if (clientCalls === 2) {
            // Опрос ушёл до PATCH и застрял в пути.
            return new Promise<Response>((resolve) => {
              releaseStalePoll = () => resolve(jsonResponse(snapshot));
            });
          }
          return jsonResponse(snapshot);
        }
        if (path.includes("/api/stats/host")) return jsonResponse({});
        return jsonResponse({}, 404);
      }),
    );

    render(<HomePage />);
    expect(await screen.findByText("Alice")).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(clientCalls).toBe(2);

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Действия для Alice" }));
    await user.click(await screen.findByRole("menuitem", { name: "Отключить" }));
    expect(await screen.findByText("Пауза")).toBeInTheDocument();

    await act(async () => {
      releaseStalePoll?.();
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(screen.getByText("Пауза")).toBeInTheDocument();
  });

  it("does not start a poll while the previous one is still in flight", async () => {
    vi.useFakeTimers({ toFake: ["setInterval"] });
    let clientCalls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        if (path.includes("/api/me")) return jsonResponse({ username: "admin", csrf: "token" });
        if (path === "/api/clients") {
          clientCalls += 1;
          if (clientCalls === 1) return jsonResponse([alice]);
          return new Promise<Response>(() => {});
        }
        if (path.includes("/api/stats/host")) return jsonResponse({});
        return jsonResponse({}, 404);
      }),
    );

    render(<HomePage />);
    expect(await screen.findByText("Alice")).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000 * 6);
    });
    expect(clientCalls).toBe(2);
  });
});

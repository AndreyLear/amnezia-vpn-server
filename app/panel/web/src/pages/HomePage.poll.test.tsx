import { act, render, screen } from "@testing-library/react";
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
});

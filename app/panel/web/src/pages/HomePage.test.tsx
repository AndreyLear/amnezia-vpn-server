import { act, render, screen } from "@testing-library/react";
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
});

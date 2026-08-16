import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ClientCard } from "@/components/ClientCard";
import type { Client } from "@/lib/api";

const base: Client = {
  id: 1,
  name: "Alice",
  description: "",
  address: "10.8.0.2/32",
  enabled: true,
  online: true,
  last_handshake_utc: "2026-08-16T00:00:00Z",
  rx_bytes: Math.round(0.1 * 1024 ** 3),
  tx_bytes: 0,
};

describe("ClientCard", () => {
  it("shows the client name and three pills for handshake, rx, and tx", () => {
    render(<ClientCard client={base} />);

    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("онлайн")).toBeInTheDocument();
    expect(screen.getByText("0,1 Гб")).toBeInTheDocument();
    expect(screen.getByText("0 Б")).toBeInTheDocument();
  });

  it("uses a muted handshake pill when the client is offline", () => {
    render(<ClientCard client={{ ...base, online: false }} />);

    expect(screen.getByText("офлайн")).toBeInTheDocument();
    expect(screen.queryByText("онлайн")).not.toBeInTheDocument();
  });
});

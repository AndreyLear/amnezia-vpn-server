import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

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
  it("shows a horizontal row with name, handshake, rx and tx and no online badge", () => {
    render(<ClientCard client={base} />);

    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("2026-08-16 00:00:00 UTC")).toBeInTheDocument();
    expect(screen.getByText("0,1 Гб")).toBeInTheDocument();
    expect(screen.getByText("0 Б")).toBeInTheDocument();
    expect(screen.queryByText("онлайн")).not.toBeInTheDocument();
    expect(screen.queryByText("офлайн")).not.toBeInTheDocument();

    const card = screen.getByText("Alice").closest("[data-slot=card]");
    expect(card).toHaveClass("flex-row");
    expect(card).not.toHaveClass("h-full", "min-h-36");
  });

  it("opens info from the name without nesting the menu in that control", async () => {
    const user = userEvent.setup();
    const onInfo = vi.fn();
    render(<ClientCard client={base} onInfo={onInfo} />);

    const menu = screen.getByRole("button", { name: "Действия для Alice" });
    const nameButton = screen.getByRole("button", { name: "Alice" });
    expect(nameButton).not.toContainElement(menu);

    await user.click(nameButton);
    expect(onInfo).toHaveBeenCalledTimes(1);
  });
});

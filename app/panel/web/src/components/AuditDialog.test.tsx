import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AuditDialog } from "@/components/AuditDialog";
import type { AuditEntry } from "@/lib/api";

// Журнал панели (amnezia-vpn-server-gqep): с записью видно, было действие
// или не было.
let entries: AuditEntry[] = [];

beforeEach(() => {
  entries = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify({ entries }), {
          headers: { "Content-Type": "application/json" },
        }),
    ),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("журнал", () => {
  it("показывает, что было сделано, кем и когда", async () => {
    entries = [
      {
        at_utc: "2026-09-07T12:00:00Z",
        actor: "admin",
        action: "client.add",
        subject: "alice",
        detail: "",
      },
    ];
    render(<AuditDialog open onOpenChange={() => {}} />);

    expect(await screen.findByText(/клиент добавлен/)).toBeInTheDocument();
    expect(screen.getByText("alice")).toBeInTheDocument();
    expect(screen.getByText("admin")).toBeInTheDocument();
    expect(screen.getByText("07.09.2026, 12:00:00")).toBeInTheDocument();
  });

  // Неудачный вход — то, ради чего в журнал заглядывают в первую очередь.
  it("выделяет неудачный вход", async () => {
    entries = [
      { at_utc: "2026-09-07T12:00:00Z", actor: "admin", action: "login.failed", subject: "", detail: "" },
    ];
    render(<AuditDialog open onOpenChange={() => {}} />);
    expect(await screen.findByText(/неудачный вход/)).toBeInTheDocument();
  });

  // «Изменён» без указания чего отвечает на вопрос наполовину.
  it("говорит, что именно изменилось", async () => {
    entries = [
      {
        at_utc: "2026-09-07T12:00:00Z",
        actor: "admin",
        action: "client.mtu",
        subject: "router",
        detail: "1420",
      },
    ];
    render(<AuditDialog open onOpenChange={() => {}} />);
    expect(await screen.findByText(/MTU клиента/)).toBeInTheDocument();
    expect(screen.getByText(/1420/)).toBeInTheDocument();
  });

  it("пустой журнал не выглядит поломкой", async () => {
    render(<AuditDialog open onOpenChange={() => {}} />);
    expect(await screen.findByText("Записей пока нет")).toBeInTheDocument();
  });

  // Имена клиентов пишут люди, и переводчик страницы принимал их за текст
  // для перевода (amnezia-vpn-server-jany).
  it("не отдаёт имена на перевод", async () => {
    entries = [
      { at_utc: "2026-09-07T12:00:00Z", actor: "admin", action: "client.add", subject: "Дом", detail: "" },
    ];
    render(<AuditDialog open onOpenChange={() => {}} />);
    const name = await screen.findByText("Дом");
    expect(name).toHaveAttribute("translate", "no");
  });
});

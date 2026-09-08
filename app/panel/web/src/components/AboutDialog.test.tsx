import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AboutDialog } from "@/components/AboutDialog";
import type { Versions } from "@/lib/api";

// Окно «О версиях» (amnezia-vpn-server-8bt5, -4yo4): всё, что определяет
// поведение сервера, в одном месте, без выдумывания неизвестных значений.
// Схема базы больше не входит в тип Versions на клиенте, но бэкенд её всё
// равно отдаёт (номер доступен через CLI) — тест воспроизводит именно это.
let answer: Partial<Versions> & { schema?: string };

function reply(versions: Partial<Versions> & { schema?: string }) {
  answer = versions;
}

beforeEach(() => {
  reply({});
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify(answer), {
          headers: { "Content-Type": "application/json" },
        }),
    ),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("о версиях", () => {
  // Владелец принял «2.10.12 — свежая» за отдельную, незнакомую версию.
  // «Последняя» говорит то же самое про тот же номер, но однозначно.
  it("называет установленную версию последней, а не свежей", async () => {
    reply({ product: "2.10.12", latest: "2.10.12" });
    render(<AboutDialog open onOpenChange={() => {}} />);

    expect(await screen.findByText("2.10.12 — последняя")).toBeInTheDocument();
    expect(screen.queryByText(/свежая/)).not.toBeInTheDocument();
  });

  it("показывает вышедшую версию отдельно от установленной", async () => {
    reply({ product: "2.10.11", latest: "2.10.12" });
    render(<AboutDialog open onOpenChange={() => {}} />);
    expect(await screen.findByText("2.10.11 — вышла 2.10.12")).toBeInTheDocument();
  });

  // Номер схемы SQLite ничего не говорит оператору, а сбой миграции панель
  // показывает отдельно — строка убрана целиком.
  it("не показывает схему базы", async () => {
    reply({ product: "2.10.12", schema: "8" });
    render(<AboutDialog open onOpenChange={() => {}} />);

    await screen.findByText("Версия");
    expect(screen.queryByText("Схема базы")).not.toBeInTheDocument();
    expect(screen.queryByText("8")).not.toBeInTheDocument();
  });
});

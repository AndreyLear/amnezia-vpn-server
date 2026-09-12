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
  // Приписка сначала была «свежая», потом «последняя», и оба раза владелец
  // читал её как вторую, незнакомую версию. Когда новой версии нет, сказать
  // нечего — номер стоит один (amnezia-vpn-server-cavu).
  it("показывает установленную версию без приписок, когда новее ничего нет", async () => {
    reply({ product: "2.10.12", latest: "2.10.12" });
    render(<AboutDialog open onOpenChange={() => {}} />);

    expect(await screen.findByText("2.10.12")).toBeInTheDocument();
    expect(screen.queryByText(/последняя|свежая/)).not.toBeInTheDocument();
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

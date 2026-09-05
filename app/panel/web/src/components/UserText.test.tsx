import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ClientCard } from "@/components/ClientCard";
import { ClientInfoDialog } from "@/components/ClientInfoDialog";
import type { Client } from "@/lib/api";

// Имена клиентов — пользовательские данные, и машинный перевод страницы
// переписывал их вместе с интерфейсом: test / router / Мама превращались в
// тест / маршрутизатор / Ни (amnezia-vpn-server-jany). Пометка проверяется на
// готовом DOM, а не в исходнике: важно, что она доехала до элемента, который
// увидит переводчик.
const client: Client = {
  id: 1,
  name: "router",
  description: "маршрутизатор в квартире",
  address: "10.8.0.4/32",
  enabled: true,
  online: true,
  last_handshake_utc: "2026-09-06T00:00:00Z",
  rx_bytes: 0,
  tx_bytes: 0,
};

function untranslatable(el: HTMLElement | null): boolean {
  const marked = el?.closest('[translate="no"]');
  return marked !== null && marked !== undefined && marked.classList.contains("notranslate");
}

describe("защита пользовательских данных от машинного перевода", () => {
  it("имя клиента в карточке помечено непереводимым", () => {
    render(<ClientCard client={client} />);

    expect(untranslatable(screen.getByText("router"))).toBe(true);
  });

  it("имя и описание в карточке клиента помечены оба", () => {
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    expect(untranslatable(screen.getByText("router"))).toBe(true);
    expect(untranslatable(screen.getByText("маршрутизатор в квартире"))).toBe(true);
  });

  it("подтверждение удаления не переводит имя, но переводит остальной текст", () => {
    render(<ClientCard client={client} />);

    const name = screen.getByText("router");
    expect(untranslatable(name)).toBe(true);
    // Сам вопрос остаётся переводимым: пользователь вправе включить перевод
    // интерфейса, и тогда должен перевестись именно интерфейс.
    const title = name.closest("h2, [data-slot='alert-dialog-title']");
    expect(title?.getAttribute("translate")).not.toBe("no");
  });
});

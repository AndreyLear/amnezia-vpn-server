import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ClientInfoDialog } from "@/components/ClientInfoDialog";
import type { Client, VlessInfo } from "@/lib/api";

const client: Client = {
  id: 7,
  name: "Alice",
  description: "phone",
  address: "10.8.0.2/32",
  address6: "",
  enabled: true,
  online: false,
  last_handshake_utc: "2026-10-27T10:00:00Z",
  rx_bytes: 0,
  tx_bytes: 0,
  mtu: 0,
  rate_limit: 0,
  dns_bypass: false,
  vless_up_bytes: 3 * 1024,
  vless_down_bytes: 5 * 1024 ** 2,
  vless_last_active_utc: "2026-10-27T09:00:00Z",
  vless_online: false,
};

const withEntry: VlessInfo = {
  enabled: true,
  routes: [
    { entry: "", prefix: "", link: "vless://a" },
    { entry: "ru", prefix: "RU ", link: "vless://b" },
  ],
};

function renderDialog(c: Client = client, load = vi.fn().mockResolvedValue(withEntry)) {
  render(
    <ClientInfoDialog
      client={c}
      onOpenChange={() => {}}
      onQr={() => {}}
      onDownload={() => {}}
      onToggle={() => {}}
      onDelete={() => {}}
      loadVless={load}
    />,
  );
  return load;
}

// amnezia-vpn-server-14y0.8: общее — над вкладками, своё у каждого
// протокола — во вкладках AWG и VLESS.
describe("ClientInfoDialog: вкладки протоколов", () => {
  it("общее стоит над вкладками и не зависит от выбранной", async () => {
    const user = userEvent.setup();
    renderDialog();

    const tablist = screen.getByRole("tablist", { name: "Протокол" });
    const common = screen.getByTestId("client-common");
    expect(common.compareDocumentPosition(tablist) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
    for (const name of ["Изменить имя", "Изменить описание", "Отключить", "Удалить"]) {
      expect(within(common).getByRole("button", { name })).toBeInTheDocument();
    }
    expect(within(common).getByText("Статус")).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: "VLESS" }));
    expect(within(common).getByRole("button", { name: "Удалить" })).toBeInTheDocument();
  });

  it("AWG: IP, конфиг, QR, MTU, скорость, рукопожатие, трафик и график", () => {
    renderDialog();

    const tab = screen.getByRole("tab", { name: "AWG" });
    expect(tab).toHaveAttribute("aria-selected", "true");
    expect(tab).toHaveAttribute("data-state", "active");
    expect(screen.getByRole("tab", { name: "VLESS" })).toHaveAttribute("aria-selected", "false");

    const panel = screen.getByRole("tabpanel");
    for (const text of ["IP", "MTU", "Ограничение скорости", "Handshake", "Трафик", "Скорость"]) {
      expect(within(panel).getByText(text)).toBeInTheDocument();
    }
    for (const name of ["Конфиг", "QR"]) {
      expect(within(panel).getByRole("button", { name })).toBeInTheDocument();
    }
    expect(within(panel).queryByText("Последняя активность")).toBeNull();
  });

  it("VLESS: маршруты, свой трафик, последняя активность и оговорка про вход", async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.click(screen.getByRole("tab", { name: "VLESS" }));
    expect(screen.getByRole("tab", { name: "VLESS" })).toHaveAttribute("aria-selected", "true");
    const panel = screen.getByRole("tabpanel");
    expect(await within(panel).findByText("Напрямую")).toBeInTheDocument();
    expect(within(panel).getByText("Через вход RU")).toBeInTheDocument();
    expect(within(panel).getByText("Трафик")).toBeInTheDocument();
    expect(within(panel).getByText("↓ 5,0 Мб · ↑ 3,0 Кб")).toBeInTheDocument();
    expect(within(panel).getByText("Последняя активность")).toBeInTheDocument();
    expect(within(panel).getByText("27 окт, 09:00:00")).toBeInTheDocument();
    expect(
      within(panel).getByText("Показан только трафик напрямую. Трафик через вход пока не учитывается"),
    ).toBeInTheDocument();
    // Ничего из AWG.
    expect(within(panel).queryByText("IP")).toBeNull();
    expect(within(panel).queryByText("MTU")).toBeNull();
  });

  it("без узлов-входов оговорки про вход нет", async () => {
    const user = userEvent.setup();
    renderDialog(client, vi.fn().mockResolvedValue({ enabled: true, routes: [withEntry.routes[0]] }));

    await user.click(screen.getByRole("tab", { name: "VLESS" }));
    expect(await screen.findByText("Напрямую")).toBeInTheDocument();
    expect(screen.getByText("Трафик")).toBeInTheDocument();
    expect(screen.queryByText(/Трафик через вход пока не учитывается/)).toBeNull();
  });

  it("VLESS не настроен — так и сказано, без пустого трафика", async () => {
    const user = userEvent.setup();
    renderDialog(client, vi.fn().mockResolvedValue({ enabled: true, routes: [] }));

    await user.click(screen.getByRole("tab", { name: "VLESS" }));
    expect(await screen.findByText("VLESS на сервере не настроен")).toBeInTheDocument();
    expect(screen.queryByText("Трафик")).toBeNull();
    expect(screen.queryByText("Последняя активность")).toBeNull();
  });

  it("открывается на вкладке протокола, по которому клиент был на связи последним", async () => {
    renderDialog({ ...client, vless_last_active_utc: "2026-10-27T11:00:00Z" });

    expect(screen.getByRole("tab", { name: "VLESS" })).toHaveAttribute("aria-selected", "true");
    expect(await screen.findByText("Напрямую")).toBeInTheDocument();
  });

  it("статус «Онлайн», если клиент на связи хотя бы по одному протоколу", () => {
    renderDialog({ ...client, online: false, vless_online: true });

    expect(within(screen.getByTestId("client-common")).getByText("Онлайн")).toBeInTheDocument();
  });
});

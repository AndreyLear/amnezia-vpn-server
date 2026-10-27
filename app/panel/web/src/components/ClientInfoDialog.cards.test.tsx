import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const fetchSpeed = vi.hoisted(() => vi.fn());
vi.mock("@/lib/api", async (orig) => ({
  ...(await orig<typeof import("@/lib/api")>()),
  fetchSpeed,
}));

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
  vless_up_bytes: 0,
  vless_down_bytes: 0,
  vless_last_active_utc: null,
  vless_online: false,
};

const routes: VlessInfo = {
  enabled: true,
  routes: [
    { entry: "", prefix: "", link: "vless://a" },
    { entry: "ru", prefix: "RU ", link: "vless://b" },
  ],
};

const entryCaption =
  "Подключения через вход не попадают в график, трафик и последнюю активность — если клиент подключается только так, здесь будет пусто";

function renderDialog(load = vi.fn().mockResolvedValue(routes)) {
  render(
    <ClientInfoDialog
      client={client}
      onOpenChange={() => {}}
      onQr={() => {}}
      onDownload={() => {}}
      onToggle={() => {}}
      onDelete={() => {}}
      loadVless={load}
    />,
  );
}

function blocks(root: HTMLElement): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>('[data-slot="info-block"]'));
}

// Кнопки содержимого окна — без крестика закрытия, который стоит вне тела.
function bodyButtons(): HTMLElement[] {
  const close = screen.getByRole("button", { name: "Close" });
  return screen.getAllByRole("button").filter((b) => b !== close);
}

beforeEach(() => {
  fetchSpeed.mockReset();
  fetchSpeed.mockResolvedValue({
    window: "10min",
    from_utc: "2026-10-27T10:00:00Z",
    to_utc: "2026-10-27T10:10:00Z",
    down_min_bps: [null],
    down_max_bps: [null],
    up_min_bps: [null],
    up_max_bps: [null],
    online: [null],
  });
});

// amnezia-vpn-server-14y0.9: окно клиента — блоками-карточками, «Удалить»
// внизу, окно шире, график и у VLESS.
describe("ClientInfoDialog: блоки", () => {
  it("общее — отдельный блок: отступ 16px, скругление 16px, рамка панели", () => {
    renderDialog();
    const common = screen.getByTestId("client-common");
    expect(common).toHaveAttribute("data-slot", "info-block");
    for (const cls of ["p-4", "rounded-[1rem]", "border", "border-border"]) {
      expect(common).toHaveClass(cls);
    }
    for (const text of ["Имя", "Описание", "Статус"]) {
      expect(within(common).getByText(text)).toBeInTheDocument();
    }
  });

  it("AWG разбит на блоки: график; IP с конфигом; MTU с ограничением; рукопожатие с трафиком", () => {
    renderDialog();
    const panel = screen.getByRole("tabpanel");
    expect(panel).toHaveClass("gap-4");
    const parts = blocks(panel);
    expect(parts).toHaveLength(4);
    expect(within(parts[0]).getByText(/^Скорость/)).toBeInTheDocument();
    expect(within(parts[1]).getByText("IP")).toBeInTheDocument();
    expect(within(parts[1]).getByRole("button", { name: "Конфиг" })).toBeInTheDocument();
    expect(within(parts[2]).getByText("MTU")).toBeInTheDocument();
    expect(within(parts[2]).getByText("Ограничение скорости")).toBeInTheDocument();
    expect(within(parts[3]).getByText("Handshake")).toBeInTheDocument();
    expect(within(parts[3]).getByText("Трафик")).toBeInTheDocument();
  });

  it("«Удалить» — в самом низу окна, после вкладок", async () => {
    const user = userEvent.setup();
    renderDialog();
    const del = screen.getByRole("button", { name: "Удалить" });
    expect(within(screen.getByTestId("client-common")).queryByRole("button", { name: "Удалить" })).toBeNull();
    const panel = screen.getByRole("tabpanel");
    expect(panel.compareDocumentPosition(del) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    const all = bodyButtons();
    expect(all[all.length - 1]).toBe(del);

    await user.click(screen.getByRole("tab", { name: "VLESS" }));
    await screen.findByText("Напрямую");
    const after = bodyButtons();
    expect(after[after.length - 1]).toBe(screen.getByRole("button", { name: "Удалить" }));
  });

  it("окно шире прежнего sm:max-w-md", () => {
    renderDialog();
    const content = document.querySelector('[data-slot="dialog-content"]');
    expect(content).toHaveClass("sm:max-w-2xl");
  });

  it("VLESS: свой график по счётчикам Xray, блоки и понятная оговорка про вход рядом с трафиком", async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.click(screen.getByRole("tab", { name: "VLESS" }));
    const panel = screen.getByRole("tabpanel");
    await within(panel).findByText("Напрямую");
    await waitFor(() => expect(fetchSpeed).toHaveBeenCalledWith(7, "10min", 120, "vless"));

    const parts = blocks(panel);
    expect(parts).toHaveLength(3);
    expect(within(parts[0]).getByText(/^Скорость/)).toBeInTheDocument();
    expect(within(parts[1]).getByText("Трафик")).toBeInTheDocument();
    expect(within(parts[1]).getByText("Последняя активность")).toBeInTheDocument();
    expect(within(parts[1]).getByText(entryCaption)).toBeInTheDocument();
    expect(within(parts[2]).getByText("Через вход RU")).toBeInTheDocument();
    expect(screen.queryByText(/пока не учитывается/)).toBeNull();
  });

  it("VLESS без входов — оговорки нет", async () => {
    const user = userEvent.setup();
    renderDialog(vi.fn().mockResolvedValue({ enabled: true, routes: [routes.routes[0]] }));
    await user.click(screen.getByRole("tab", { name: "VLESS" }));
    await screen.findByText("Напрямую");
    expect(screen.queryByText(entryCaption)).toBeNull();
  });

  it("AWG-график просит ряды AWG", async () => {
    renderDialog();
    await waitFor(() => expect(fetchSpeed).toHaveBeenCalledWith(7, "10min", 120, "awg"));
  });
});

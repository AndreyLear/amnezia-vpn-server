import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ClientInfoDialog } from "@/components/ClientInfoDialog";
import type { Client, VlessInfo } from "@/lib/api";

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock("sonner", () => ({
  toast: { success: (m: string) => toastSuccess(m), error: (m: string) => toastError(m) },
}));

const client: Client = {
  id: 7,
  name: "Alice",
  description: "",
  address: "10.8.0.2/32",
  enabled: true,
  online: false,
  last_handshake_utc: null,
  rx_bytes: 0,
  tx_bytes: 0,
  // Был на связи по VLESS — окно открывается сразу на вкладке VLESS
  // (amnezia-vpn-server-14y0.8).
  vless_last_active_utc: "2026-10-27T10:00:00Z",
};

const direct = "vless://uuid@vpn.example.com:443?type=xhttp#VPN%20Alice";
const viaRu = "vless://uuid@ru.example.com:443?type=xhttp#RU%20Alice";

const twoRoutes: VlessInfo = {
  enabled: true,
  routes: [
    { entry: "", prefix: "", link: direct },
    { entry: "ru", prefix: "RU ", link: viaRu },
  ],
};

afterEach(() => {
  toastSuccess.mockReset();
  toastError.mockReset();
});

// amnezia-vpn-server-14y0.5: рядом с конфигом AWG — ссылки VLESS по
// маршрутам, у каждой «Ссылка» и «QR».
describe("VLESS в окне клиента", () => {
  it("показывает маршруты с подписями и пояснениями", async () => {
    const load = vi.fn().mockResolvedValue(twoRoutes);
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} loadVless={load} />);

    expect(await screen.findByText("Напрямую")).toBeInTheDocument();
    expect(screen.getByText("Через вход")).toBeInTheDocument();
    expect(
      screen.getByText("Подходит для Wi-Fi и домашнего интернета, на мобильном работает не всегда"),
    ).toBeInTheDocument();
    expect(load).toHaveBeenCalledWith(7);
    // Ссылка — секрет: в разметке её нет, только по кнопке.
    expect(document.body.innerHTML).not.toContain("vless://");
  });

  it("копирует ссылку маршрута", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    render(
      <ClientInfoDialog client={client} onOpenChange={() => {}} loadVless={() => Promise.resolve(twoRoutes)} />,
    );

    await user.click(await screen.findByRole("button", { name: "Скопировать ссылку: Через вход" }));
    expect(writeText).toHaveBeenCalledWith(viaRu);
    expect(toastSuccess).toHaveBeenCalledWith("Ссылка скопирована");
  });

  it("без буфера обмена советует QR", async () => {
    const user = userEvent.setup();
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
    render(
      <ClientInfoDialog client={client} onOpenChange={() => {}} loadVless={() => Promise.resolve(twoRoutes)} />,
    );

    await user.click(await screen.findByRole("button", { name: "Скопировать ссылку: Напрямую" }));
    expect(toastError).toHaveBeenCalledWith("Не удалось скопировать ссылку, откройте QR");
  });

  it("QR рисует сервер для выбранного маршрута", async () => {
    const user = userEvent.setup();
    render(
      <ClientInfoDialog client={client} onOpenChange={() => {}} loadVless={() => Promise.resolve(twoRoutes)} />,
    );

    await user.click(await screen.findByRole("button", { name: "QR: Через вход" }));
    expect(await screen.findByRole("heading", { name: "QR-код VLESS: Alice" })).toBeInTheDocument();
    const img = screen.getByAltText("QR-код VLESS клиента Alice, маршрут «Через вход»");
    expect(img).toHaveAttribute("src", "/api/clients/7/vless/qr?entry=ru");
  });

  it("у клиента на паузе ссылок нет", async () => {
    render(
      <ClientInfoDialog
        client={{ ...client, enabled: false }}
        onOpenChange={() => {}}
        loadVless={() => Promise.resolve({ enabled: false, routes: [] })}
      />,
    );

    expect(
      await screen.findByText("Клиент на паузе — ссылки VLESS появятся после включения"),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Скопировать ссылку/ })).toBeNull();
  });

  it("без Xray на сервере так и сказано, ссылок нет", async () => {
    const load = vi.fn().mockResolvedValue({ enabled: true, routes: [] });
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} loadVless={load} />);

    await waitFor(() => expect(load).toHaveBeenCalled());
    expect(await screen.findByText("VLESS на сервере не настроен")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Скопировать ссылку/ })).toBeNull();
  });

  it("ошибка загрузки названа", async () => {
    render(
      <ClientInfoDialog
        client={client}
        onOpenChange={() => {}}
        loadVless={() => Promise.reject(new Error("boom"))}
      />,
    );

    expect(
      await screen.findByText("Не удалось загрузить ссылки VLESS, откройте окно клиента заново"),
    ).toBeInTheDocument();
  });

  it("перезагружает ссылки, когда клиента включили", async () => {
    const load = vi
      .fn()
      .mockResolvedValueOnce({ enabled: false, routes: [] })
      .mockResolvedValueOnce(twoRoutes);
    const { rerender } = render(
      <ClientInfoDialog client={{ ...client, enabled: false }} onOpenChange={() => {}} loadVless={load} />,
    );
    await screen.findByText("Клиент на паузе — ссылки VLESS появятся после включения");

    rerender(<ClientInfoDialog client={client} onOpenChange={() => {}} loadVless={load} />);
    expect(await screen.findByText("Напрямую")).toBeInTheDocument();
    expect(load).toHaveBeenCalledTimes(2);
  });
});

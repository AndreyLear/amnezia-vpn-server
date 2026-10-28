import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { QrDialog } from "@/components/QrDialog";
import type { VlessInfo } from "@/lib/api";

const withEntry: VlessInfo = {
  enabled: true,
  routes: [
    { entry: "", prefix: "", link: "vless://a" },
    { entry: "ru", prefix: "RU ", link: "vless://b" },
  ],
};

function renderQr(
  protocol: "awg" | "vless",
  load: (id: number) => Promise<VlessInfo> = vi.fn().mockResolvedValue(withEntry),
) {
  render(
    <QrDialog
      clientId={7}
      clientName="Alice"
      initialProtocol={protocol}
      loadVless={load}
      onOpenChange={() => {}}
    />,
  );
  return load;
}

const vlessHint = "Отсканируйте QR в v2RayTun, Happ или другом приложении с поддержкой VLESS";

// amnezia-vpn-server-14y0.13: пункт «QR-код» строки клиента показывал только
// AWG. Теперь в окне выбор протокола, а у VLESS — маршрута.
describe("QrDialog: AWG и VLESS", () => {
  it("открывается на переданном протоколе; AWG — прежний QR и подпись", () => {
    renderQr("awg");
    const proto = screen.getByRole("group", { name: "Протокол" });
    expect(proto).toHaveAttribute("data-size", "md");
    expect(screen.getByRole("button", { name: "AWG" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("img", { name: "QR-код AmneziaWG клиента Alice" })).toHaveAttribute(
      "src",
      "/clients/7/qr",
    );
    expect(screen.getByText(/AmneziaWG 2.0 и выше/)).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Маршрут" })).toBeNull();
  });

  it("VLESS: выбор маршрута вторым уровнем, подсказка маршрута и QR выбранного", async () => {
    const user = userEvent.setup();
    const load = renderQr("vless");
    expect(screen.getByRole("button", { name: "VLESS" })).toHaveAttribute("aria-pressed", "true");
    const route = await screen.findByRole("group", { name: "Маршрут" });
    expect(load).toHaveBeenCalledWith(7);
    expect(route).toHaveAttribute("data-size", "sm");
    expect(screen.getByText(vlessHint)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Напрямую" })).toHaveAttribute("aria-pressed", "true");
    expect(
      screen.getByText("Подходит для Wi-Fi и домашнего интернета, на мобильном работает не всегда"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("img", { name: "QR-код VLESS клиента Alice, маршрут «Напрямую»" }),
    ).toHaveAttribute("src", "/api/clients/7/vless/qr");

    await user.click(screen.getByRole("button", { name: "Через вход RU" }));
    expect(
      screen.getByRole("img", { name: "QR-код VLESS клиента Alice, маршрут «Через вход RU»" }),
    ).toHaveAttribute("src", "/api/clients/7/vless/qr?entry=ru");
    expect(screen.getByText(/Для мобильного интернета/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "AWG" }));
    expect(screen.getByRole("img", { name: "QR-код AmneziaWG клиента Alice" })).toBeInTheDocument();
  });

  it("VLESS без входов — маршрут не выбирают", async () => {
    renderQr("vless", vi.fn().mockResolvedValue({ enabled: true, routes: [withEntry.routes[0]] }));
    expect(
      await screen.findByRole("img", { name: "QR-код VLESS клиента Alice, маршрут «Напрямую»" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Маршрут" })).toBeNull();
  });

  it("VLESS: пауза, не настроен и ошибка — так и сказано, без QR", async () => {
    const { unmount } = render(
      <QrDialog
        clientId={7}
        clientName="Alice"
        initialProtocol="vless"
        loadVless={vi.fn().mockResolvedValue({ enabled: false, routes: [] })}
        onOpenChange={() => {}}
      />,
    );
    expect(await screen.findByText("Клиент на паузе — QR VLESS появится после включения")).toBeInTheDocument();
    expect(screen.queryByRole("img")).toBeNull();
    unmount();

    renderQr("vless", vi.fn().mockResolvedValue({ enabled: true, routes: [] }));
    expect(await screen.findByText("VLESS на сервере не настроен")).toBeInTheDocument();
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("VLESS: сбой загрузки", async () => {
    renderQr("vless", vi.fn().mockRejectedValue(new Error("down")));
    expect(await screen.findByText("Не удалось загрузить QR VLESS, откройте окно заново")).toBeInTheDocument();
  });
});

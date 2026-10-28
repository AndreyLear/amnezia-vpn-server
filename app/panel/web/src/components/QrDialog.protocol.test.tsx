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
    expect(screen.queryByRole("group", { name: "Маршрут" })).toBeNull();
    // Пояснений нет и на AWG — окно единообразно (amnezia-vpn-server-14y0.17).
    expect(screen.queryByText(/Отсканируйте|AmneziaWG 2.0/)).toBeNull();
  });

  it("VLESS: выбор маршрута вторым уровнем без пояснений и QR выбранного", async () => {
    const user = userEvent.setup();
    const load = renderQr("vless");
    expect(screen.getByRole("button", { name: "VLESS" })).toHaveAttribute("aria-pressed", "true");
    const route = await screen.findByRole("group", { name: "Маршрут" });
    expect(load).toHaveBeenCalledWith(7);
    expect(route).toHaveAttribute("data-size", "sm");
    expect(screen.getByRole("button", { name: "Напрямую" })).toHaveAttribute("aria-pressed", "true");
    // Пояснений нет: ни подзаголовка, ни строки под маршрутом
    // (amnezia-vpn-server-14y0.17).
    expect(screen.queryByText(/Отсканируйте|Подходит/)).toBeNull();
    expect(
      screen.getByRole("img", { name: "QR-код VLESS клиента Alice, маршрут «Напрямую»" }),
    ).toHaveAttribute("src", "/api/clients/7/vless/qr");

    expect(screen.getByRole("button", { name: "Каскад" })).toHaveAttribute("aria-pressed", "false");
    await user.click(screen.getByRole("button", { name: "Каскад" }));
    // Выбранный маршрут виден: aria-pressed и фон переходят на «Каскад»
    // (amnezia-vpn-server-14y0.17).
    expect(screen.getByRole("button", { name: "Каскад" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "Каскад" })).toHaveClass("bg-secondary");
    expect(screen.getByRole("button", { name: "Напрямую" })).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByRole("button", { name: "Напрямую" })).not.toHaveClass("bg-secondary");
    expect(
      screen.getByRole("img", { name: "QR-код VLESS клиента Alice, маршрут «Каскад»" }),
    ).toHaveAttribute("src", "/api/clients/7/vless/qr?entry=ru");
    expect(screen.queryByText(/Подходит/)).toBeNull();

    await user.click(screen.getByRole("button", { name: "AWG" }));
    expect(screen.getByRole("img", { name: "QR-код AmneziaWG клиента Alice" })).toBeInTheDocument();
  });

  // amnezia-vpn-server-14y0.17: маршрут стоял отдельной строкой под
  // вкладками. Теперь одна строка: вкладки слева, маршрут справа; на узком
  // экране маршрут переносится вниз, но остаётся справа (ml-auto), а вкладки
  // не ломаются. Шапка → содержимое 24px, строка → QR 16px (правила UI).
  it("вкладки и маршрут — одна строка: протокол слева, маршрут справа", async () => {
    renderQr("vless");
    const route = await screen.findByRole("group", { name: "Маршрут" });
    const proto = screen.getByRole("group", { name: "Протокол" });
    const row = proto.parentElement!;
    expect(route.parentElement).toBe(row);
    expect(row.firstElementChild).toBe(proto);
    expect(row).toHaveClass("flex", "flex-wrap", "items-center", "justify-between", "gap-4");
    expect(route).toHaveClass("ml-auto");
    // Строка и QR — одна группа с шагом 16px, от шапки её отделяют 24px.
    const img = screen.getByRole("img", { name: /маршрут «Напрямую»/ });
    expect(img.parentElement).toBe(row.parentElement);
    expect(row.parentElement).toHaveClass("grid", "gap-4");
    expect(row.parentElement!.closest('[data-slot="dialog-body"]')).toHaveClass("gap-6");
  });

  it("на AWG справа от вкладок пусто", () => {
    renderQr("awg");
    const proto = screen.getByRole("group", { name: "Протокол" });
    expect(proto.parentElement!.children).toHaveLength(1);
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

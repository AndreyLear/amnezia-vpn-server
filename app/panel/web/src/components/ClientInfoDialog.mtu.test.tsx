import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ClientInfoDialog } from "@/components/ClientInfoDialog";
import type { Client } from "@/lib/api";

// Свой MTU у клиента (amnezia-vpn-server-h2pg). Серверное значение рассчитано
// на худшую последнюю милю среди всех; клиенту с заведомо лучшим каналом его
// можно поднять — и панель должна показывать, откуда взято действующее.
const client: Client = {
  id: 7,
  name: "router",
  description: "",
  address: "10.8.0.4/32",
  enabled: true,
  online: true,
  last_handshake_utc: null,
  rx_bytes: 0,
  tx_bytes: 0,
  mtu: 0,
};

describe("MTU клиента", () => {
  it("без своего значения говорит, что берёт серверное", () => {
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    expect(screen.getByText("MTU")).toBeInTheDocument();
    expect(screen.getByText("Как у сервера")).toBeInTheDocument();
  });

  // amnezia-vpn-server-4cnf: the owner's screenshots showed this fallback
  // value starting with a lowercase letter ("как у сервера"). It stands on
  // its own in the read-only property row (not mid-sentence text like the
  // hint below the input), so it must start with a capital letter.
  it("capitalizes the MTU fallback value", () => {
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    expect(screen.getByText("Как у сервера")).toBeInTheDocument();
    expect(screen.queryByText("как у сервера")).toBeNull();
  });

  it("со своим значением показывает его", () => {
    render(
      <ClientInfoDialog client={{ ...client, mtu: 1420 }} onOpenChange={() => {}} />,
    );

    expect(screen.getByText("1420")).toBeInTheDocument();
    expect(screen.queryByText("Как у сервера")).toBeNull();
  });

  // Владелец прочитал первую версию подсказки и спросил ровно то, чего в ней
  // не было: можно ли поменять MTU в уже выданном конфиге, не выдавая новый.
  // Ответ — можно, вручную в приложении, и он обязан быть в подсказке.
  it("подсказка отвечает, что делать тем, кто уже подключён", async () => {
    const user = userEvent.setup();
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    await user.click(screen.getByRole("button", { name: "Изменить MTU" }));

    // Подсказка живёт в диалоге правки; тот же текст (в другом регистре)
    // встречается и в строке свойства, поэтому ищем внутри самого диалога.
    const dialog = await screen.findByRole("dialog", { name: "MTU" });
    expect(within(dialog).getByText(/от 1280 до 1440/)).toBeInTheDocument();
    expect(
      within(dialog).getByText(/вписать вручную в приложении/),
    ).toBeInTheDocument();
  });

  // Владелец: «нужно добавить ограничение, чтобы нельзя было поставить любое
  // значение». Отказ сервера — последняя линия, но человек не должен
  // упираться в неё, набрав 9000 и нажав «Сохранить».
  it("не даёт сохранить значение вне границ", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(true);
    render(<ClientInfoDialog client={client} onSave={onSave} onOpenChange={() => {}} />);

    await user.click(screen.getByRole("button", { name: "Изменить MTU" }));
    const field = await screen.findByLabelText("Размер пакета, байт");
    await user.type(field, "9000");

    const save = screen.getByRole("button", { name: "Сохранить MTU" });
    expect(save).toBeDisabled();
    const dialog = screen.getByRole("dialog", { name: "MTU" });
    expect(within(dialog).getByText(/от 1280 до 1440/)).toBeInTheDocument();

    await user.clear(field);
    await user.type(field, "1420");
    expect(save).toBeEnabled();
  });

  it("поле объявляет границы и браузеру", async () => {
    const user = userEvent.setup();
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    await user.click(screen.getByRole("button", { name: "Изменить MTU" }));
    const field = await screen.findByLabelText("Размер пакета, байт");

    expect(field).toHaveAttribute("type", "number");
    expect(field).toHaveAttribute("min", "1280");
    expect(field).toHaveAttribute("max", "1440");
  });

  it("сохраняет введённое значение", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(true);
    render(<ClientInfoDialog client={client} onSave={onSave} onOpenChange={() => {}} />);

    await user.click(screen.getByRole("button", { name: "Изменить MTU" }));
    const field = await screen.findByLabelText("Размер пакета, байт");
    await user.type(field, "1420");
    await user.click(screen.getByRole("button", { name: "Сохранить MTU" }));

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith(
        expect.objectContaining({ mtu: 1420 }),
      );
    });
  });

  // Пустое поле — способ вернуть клиента к серверному значению. Без этого
  // своё значение нельзя было бы снять, только заменить другим.
  it("пустое поле снимает своё значение", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(true);
    render(
      <ClientInfoDialog
        client={{ ...client, mtu: 1420 }}
        onSave={onSave}
        onOpenChange={() => {}}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Изменить MTU" }));
    const field = await screen.findByLabelText("Размер пакета, байт");
    await user.clear(field);
    await user.click(screen.getByRole("button", { name: "Сохранить MTU" }));

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ mtu: 0 }));
    });
  });
});

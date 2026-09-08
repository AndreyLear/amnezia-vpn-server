import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ClientInfoDialog } from "@/components/ClientInfoDialog";
import type { Client } from "@/lib/api";

// Предел скорости на клиента (amnezia-vpn-server-jzzu). Он сокращает потери,
// а не ускоряет, и текст обязан говорить именно это.
function client(overrides: Partial<Client> = {}): Client {
  return {
    id: 1,
    name: "router",
    description: "",
    address: "10.8.0.4/32",
    address6: "",
    enabled: true,
    online: true,
    last_handshake_utc: null,
    rx_bytes: 0,
    tx_bytes: 0,
    mtu: 0,
    dns_bypass: false,
    rate_limit: 0,
    ...overrides,
  };
}

describe("предел скорости в карточке клиента", () => {
  it("без предела так и написано", () => {
    render(<ClientInfoDialog client={client()} onOpenChange={() => {}} />);
    expect(screen.getByText("без предела")).toBeInTheDocument();
  });

  it("заданный предел показан с единицами", () => {
    render(<ClientInfoDialog client={client({ rate_limit: 50 })} onOpenChange={() => {}} />);
    expect(screen.getByText("50 Мбит/с")).toBeInTheDocument();
  });

  // Самое важное в этом окне — не поле, а подпись: для чего это и что даёт.
  it("окно правки говорит, зачем предел нужен", async () => {
    const user = userEvent.setup();
    render(<ClientInfoDialog client={client()} onOpenChange={() => {}} />);

    await user.click(screen.getByRole("button", { name: "Изменить предел скорости" }));
    // Польза названа тем, что человек увидит: ровный поток вместо рывков.
    // Прежний текст обещал прежнюю скорость, и замер это опроверг
    // (amnezia-vpn-server-ouhb).
    expect(await screen.findByText(/Держит скорость ровной/)).toBeInTheDocument();
    expect(screen.getByText(/Видео не встаёт/)).toBeInTheDocument();
    // И как ограничение снять.
    expect(screen.getByText(/Оставьте поле пустым/)).toBeInTheDocument();
  });

  it("сохраняет заданное значение", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(true);
    render(<ClientInfoDialog client={client()} onOpenChange={() => {}} onSave={onSave} />);

    await user.click(screen.getByRole("button", { name: "Изменить предел скорости" }));
    await user.type(await screen.findByLabelText("Мегабит в секунду"), "50");
    await user.click(screen.getByRole("button", { name: "Сохранить предел скорости" }));

    await waitFor(() =>
      expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ rate_limit: 50 })),
    );
  });

  // Значение вне границ сервер отвергнет; человек не должен упираться в отказ,
  // уже нажав «Сохранить».
  it("не даёт сохранить значение вне границ", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(true);
    render(<ClientInfoDialog client={client()} onOpenChange={() => {}} onSave={onSave} />);

    await user.click(screen.getByRole("button", { name: "Изменить предел скорости" }));
    await user.type(await screen.findByLabelText("Мегабит в секунду"), "5000");
    expect(screen.getByRole("button", { name: "Сохранить предел скорости" })).toBeDisabled();
    expect(onSave).not.toHaveBeenCalled();
  });

  // Пустое поле — снятие предела, а не ошибка.
  it("пустое поле снимает предел", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(true);
    render(<ClientInfoDialog client={client({ rate_limit: 50 })} onOpenChange={() => {}} onSave={onSave} />);

    await user.click(screen.getByRole("button", { name: "Изменить предел скорости" }));
    await user.clear(await screen.findByLabelText("Мегабит в секунду"));
    await user.click(screen.getByRole("button", { name: "Сохранить предел скорости" }));

    await waitFor(() =>
      expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ rate_limit: 0 })),
    );
  });
});

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ClientInfoDialog } from "@/components/ClientInfoDialog";
import type { Client } from "@/lib/api";

// Ограничение скорости на клиента (amnezia-vpn-server-jzzu). Оно сокращает
// потери, а не ускоряет, и текст обязан говорить именно это.
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

describe("ограничение скорости в карточке клиента", () => {
  it("без ограничений так и написано", () => {
    render(<ClientInfoDialog client={client()} onOpenChange={() => {}} />);
    expect(screen.getByText("Без ограничений")).toBeInTheDocument();
  });

  it("заданное ограничение показано с единицами", () => {
    render(<ClientInfoDialog client={client({ rate_limit: 50 })} onOpenChange={() => {}} />);
    expect(screen.getByText("50 Мбит/с")).toBeInTheDocument();
  });

  // Самое важное в этом окне — не поле, а подпись: для чего это и что даёт.
  // Текст владельца — дословно, включая обе точки внутри абзаца
  // (amnezia-vpn-server-yjh2): это не «одиночное завершающее предложение»,
  // на которое распространяется правило «без точки», а два предложения
  // владельца, и они должны остаться как он их написал.
  it("окно правки говорит, зачем ограничение нужно", async () => {
    const user = userEvent.setup();
    render(<ClientInfoDialog client={client()} onOpenChange={() => {}} />);

    await user.click(screen.getByRole("button", { name: "Изменить ограничение скорости" }));
    expect(
      await screen.findByText(
        "Тесты показали, что ограничение скорости может помочь выровнять кривую загрузки и сделать потребление трафика более равномерным. Кроме того, такой подход потенциально может улучшить стабильность работы при загрузке видео и аудио, а также при использовании видеозвонков.",
      ),
    ).toBeInTheDocument();
    // И как ограничение снять.
    expect(screen.getByText(/Оставьте поле пустым/)).toBeInTheDocument();
  });

  it("сохраняет заданное значение", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(true);
    render(<ClientInfoDialog client={client()} onOpenChange={() => {}} onSave={onSave} />);

    await user.click(screen.getByRole("button", { name: "Изменить ограничение скорости" }));
    await user.type(await screen.findByLabelText("Мегабит в секунду"), "50");
    await user.click(screen.getByRole("button", { name: "Сохранить ограничение скорости" }));

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

    await user.click(screen.getByRole("button", { name: "Изменить ограничение скорости" }));
    await user.type(await screen.findByLabelText("Мегабит в секунду"), "5000");
    expect(screen.getByRole("button", { name: "Сохранить ограничение скорости" })).toBeDisabled();
    expect(onSave).not.toHaveBeenCalled();
  });

  // Пустое поле — снятие ограничения, а не ошибка.
  it("пустое поле снимает ограничение", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(true);
    render(<ClientInfoDialog client={client({ rate_limit: 50 })} onOpenChange={() => {}} onSave={onSave} />);

    await user.click(screen.getByRole("button", { name: "Изменить ограничение скорости" }));
    await user.clear(await screen.findByLabelText("Мегабит в секунду"));
    await user.click(screen.getByRole("button", { name: "Сохранить ограничение скорости" }));

    await waitFor(() =>
      expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ rate_limit: 0 })),
    );
  });

  // Владелец: «иногда ограничение скорости применяется долго, нужно не
  // просто дизейблить кнопку, а показать спиннер — показать, что не
  // зависло, а работает» (amnezia-vpn-server-yjh2). A disabled-but-static
  // button looks identical whether it's mid-save or just stuck.
  it("показывает вертушку на кнопке, пока ограничение применяется", async () => {
    const user = userEvent.setup();
    const c = client();
    const { rerender } = render(
      <ClientInfoDialog client={c} onOpenChange={() => {}} />,
    );

    await user.click(screen.getByRole("button", { name: "Изменить ограничение скорости" }));
    const dialog = await screen.findByRole("dialog", { name: "Ограничение скорости" });
    // No spinner before the save starts.
    expect(within(dialog).queryByRole("status")).toBeNull();

    // `pending` comes from the parent (HomePage keys it off the client id
    // that's mid-mutation), so it's simulated here by re-rendering with it
    // true rather than by resolving onSave — the component itself has no
    // internal "saving" state to drive.
    rerender(<ClientInfoDialog client={c} pending onOpenChange={() => {}} />);

    expect(within(dialog).getByRole("status")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Сохранить ограничение скорости" })).toBeDisabled();
  });

  // Владелец: «снова не понятно что будет, если нажать на кнопку крестик».
  // Факт: запрос уже ушёл на сервер, закрытие окна его не отменяет —
  // ограничение применится всё равно. Поэтому пока идёт применение, крестик
  // — видимо неактивная кнопка (disabled, не просто игнорируемый клик), а не
  // способ отменить операцию, которую отменить уже нельзя
  // (amnezia-vpn-server-yjh2).
  it("крестик недоступен, пока ограничение применяется, и не закрывает окно", async () => {
    const user = userEvent.setup();
    const c = client();
    const { rerender } = render(
      <ClientInfoDialog client={c} onOpenChange={() => {}} />,
    );

    await user.click(screen.getByRole("button", { name: "Изменить ограничение скорости" }));
    rerender(<ClientInfoDialog client={c} pending onOpenChange={() => {}} />);

    const dialog = screen.getByRole("dialog", { name: "Ограничение скорости" });
    const close = within(dialog).getByRole("button", { name: "Close" });
    expect(close).toBeDisabled();

    await user.click(close);
    expect(screen.getByRole("dialog", { name: "Ограничение скорости" })).toBeInTheDocument();

    // Once the save settles, the same X works normally again. Scoped to
    // the rate dialog specifically: the outer "Клиент" dialog underneath
    // has its own Close button, so an unscoped query would be ambiguous.
    rerender(<ClientInfoDialog client={c} pending={false} onOpenChange={() => {}} />);
    await user.click(
      within(screen.getByRole("dialog", { name: "Ограничение скорости" })).getByRole("button", {
        name: "Close",
      }),
    );
    expect(screen.queryByRole("dialog", { name: "Ограничение скорости" })).not.toBeInTheDocument();
  });
});

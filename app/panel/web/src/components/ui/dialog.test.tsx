import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { Dialog, DialogContent, DialogHeader, DialogTitle } from "./dialog";

// Окно без единого своего поля (Журнал, Состояние служб): раньше Radix
// переносил фокус на крестик сразу при открытии, и вокруг него было видно
// кольцо фокуса, хотя человек ещё не тронул клавиатуру
// (amnezia-vpn-server-suni).
describe("фокус при открытии окна без своих полей (amnezia-vpn-server-suni)", () => {
  it("не ставит фокус на крестик при открытии", () => {
    render(
      <Dialog open onOpenChange={() => {}}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Заголовок</DialogTitle>
          </DialogHeader>
          <p>Только текст, фокусировать нечего</p>
        </DialogContent>
      </Dialog>,
    );

    expect(screen.getByRole("button", { name: "Close" })).not.toHaveFocus();
  });

  it("оставляет обход по Tab рабочим — крестик получает обычный видимый фокус", async () => {
    const user = userEvent.setup();
    render(
      <Dialog open onOpenChange={() => {}}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Заголовок</DialogTitle>
          </DialogHeader>
          <p>Только текст, фокусировать нечего</p>
        </DialogContent>
      </Dialog>,
    );

    await user.tab();
    expect(screen.getByRole("button", { name: "Close" })).toHaveFocus();
  });
});

// Карточка клиента: заголовок «Клиент», крестик и переключатель графика
// уезжали вверх вместе с содержимым при прокрутке (amnezia-vpn-server-5oj5).
describe("шапка не прокручивается вместе с содержимым (amnezia-vpn-server-5oj5)", () => {
  it("держит заголовок и крестик вне прокручиваемой обёртки", () => {
    render(
      <Dialog open onOpenChange={() => {}}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Заголовок</DialogTitle>
          </DialogHeader>
          <div data-testid="long-content">Длинное содержимое</div>
        </DialogContent>
      </Dialog>,
    );

    const body = screen
      .getByTestId("long-content")
      .closest('[data-slot="dialog-body"]');
    expect(body).not.toBeNull();
    expect(body).toHaveClass("overflow-y-auto");

    const header = screen
      .getByText("Заголовок")
      .closest('[data-slot="dialog-header"]');
    expect(header).not.toBeNull();
    expect(header).toHaveClass("sticky", "top-0");
    // Хедер физически внутри прокручиваемой обёртки — иначе его top-0
    // некуда прикреплять, — но закреплён в ней, а не течёт вместе с ней.
    expect(body).toContainElement(header as HTMLElement);

    const closeButton = screen.getByRole("button", { name: "Close" });
    // А крестик вне обёртки целиком: прокрутка body никогда его не унесёт.
    expect(body).not.toContainElement(closeButton);
  });
});

// Отступ тела переехал вместе со скроллом на отдельную обёртку (dialog-body),
// а className по-прежнему уходил только на внешний элемент — тот, что больше
// не владеет расстоянием между заголовком и содержимым. В итоге любой вызов
// вида <DialogContent className="gap-4"> молча переставал на что-либо
// влиять, а QrDialog и карточка клиента (у них className вовсе без gap-*)
// без спроса получили чужой отступ по умолчанию (amnezia-vpn-server-5oj5).
describe("className по-прежнему задаёт отступ тела, а не только примитив", () => {
  it("без className в теле остаётся отступ по умолчанию 16px (gap-4), как до правки шапки", () => {
    render(
      <Dialog open onOpenChange={() => {}}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Заголовок</DialogTitle>
          </DialogHeader>
          <p data-testid="content">Содержимое</p>
        </DialogContent>
      </Dialog>,
    );

    const body = screen
      .getByTestId("content")
      .closest('[data-slot="dialog-body"]');
    expect(body).toHaveClass("gap-4");
    expect(body).not.toHaveClass("gap-6");
  });

  it("className из вызова всё ещё меняет отступ тела, а не только внешнего элемента", () => {
    render(
      <Dialog open onOpenChange={() => {}}>
        <DialogContent className="gap-6">
          <DialogHeader>
            <DialogTitle>Заголовок</DialogTitle>
          </DialogHeader>
          <p data-testid="content">Содержимое</p>
        </DialogContent>
      </Dialog>,
    );

    // Проверяем именно gap-6 (не gap-4 по умолчанию для тела): если бы
    // className уходил только на внешний элемент — как было в регрессии, —
    // тело осталось бы на gap-4 несмотря на явный запрос вызова.
    const body = screen
      .getByTestId("content")
      .closest('[data-slot="dialog-body"]');
    expect(body).toHaveClass("gap-6");
    expect(body).not.toHaveClass("gap-4");
  });
});

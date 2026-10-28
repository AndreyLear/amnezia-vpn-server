import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { Segment } from "@/components/Segment";
import { Tabs, TabsContent } from "@/components/ui/tabs";

const options = [
  { value: "a", label: "Первый" },
  { value: "b", label: "Второй" },
] as const;

// amnezia-vpn-server-14y0.13: один сегмент на панель, двух уровней; по
// содержимому, а не на всю ширину.
describe("Segment", () => {
  it("кнопки: выбранный — aria-pressed, выбор уходит наверх", async () => {
    const onValueChange = vi.fn();
    render(<Segment label="Выбор" options={[...options]} value="a" onValueChange={onValueChange} />);
    const group = screen.getByRole("group", { name: "Выбор" });
    expect(group).toHaveAttribute("data-slot", "segment");
    expect(group).toHaveClass("w-fit");
    expect(group).not.toHaveClass("w-full");
    expect(screen.getByRole("button", { name: "Первый" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "Второй" })).toHaveAttribute("aria-pressed", "false");
    await userEvent.setup().click(screen.getByRole("button", { name: "Второй" }));
    expect(onValueChange).toHaveBeenCalledWith("b");
  });

  // amnezia-vpn-server-14y0.17: после клика мышью кнопка оставалась в фокусе,
  // и любое нажатие клавиши (хоть Cmd+Shift+4 для скриншота) включало
  // :focus-visible — вокруг AWG/VLESS вставал толстый контур. Мышью фокус на
  // кнопку не переносится; с клавиатуры — как раньше.
  it("кнопки: клик мышью не забирает фокус, клавиатура — забирает и выбирает", async () => {
    const onValueChange = vi.fn();
    render(<Segment label="Выбор" options={[...options]} value="a" onValueChange={onValueChange} />);
    const second = screen.getByRole("button", { name: "Второй" });
    // false — mousedown отменён, браузер не переносит фокус на кнопку.
    expect(fireEvent.mouseDown(second)).toBe(false);
    const user = userEvent.setup();
    await user.click(second);
    expect(onValueChange).toHaveBeenLastCalledWith("b");
    expect(document.activeElement).not.toBe(second);

    onValueChange.mockClear();
    await user.tab();
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Первый" }));
    await user.tab();
    expect(document.activeElement).toBe(second);
    await user.keyboard("{Enter}");
    expect(onValueChange).toHaveBeenCalledWith("b");
  });

  // amnezia-vpn-server-14y0.17: под курсором невыбранный вариант получал
  // фон и полный цвет текста — почти как выбранный, и в окне QR было не
  // понять, какой маршрут выбран. Фон — только у выбранного; наведение лишь
  // проявляет текст.
  it("фон только у выбранного, наведение не делает вариант похожим на выбранный", () => {
    render(<Segment label="Выбор" size="sm" options={[...options]} value="a" onValueChange={() => {}} />);
    const active = screen.getByRole("button", { name: "Первый" });
    const idle = screen.getByRole("button", { name: "Второй" });
    expect(active).toHaveAttribute("aria-pressed", "true");
    expect(active).toHaveClass("bg-secondary", "text-secondary-foreground");
    expect(idle).toHaveAttribute("aria-pressed", "false");
    expect(idle).toHaveClass("text-muted-foreground", "hover:text-foreground", "hover:bg-transparent", "dark:hover:bg-transparent");
    expect(idle.className).not.toMatch(/(^|\s)(dark:)?hover:bg-muted/);
    expect(idle).not.toHaveClass("bg-secondary");
  });

  it("первый уровень выше второго", () => {
    render(
      <>
        <Segment label="Первый уровень" options={[...options]} value="a" onValueChange={() => {}} />
        <Segment label="Второй уровень" size="sm" options={[...options]} value="a" onValueChange={() => {}} />
      </>,
    );
    const [md] = screen.getAllByRole("button", { name: "Первый" });
    const sm = screen.getAllByRole("button", { name: "Первый" })[1];
    expect(md).toHaveClass("h-8");
    expect(sm).toHaveClass("h-7");
    expect(screen.getByRole("group", { name: "Первый уровень" })).toHaveAttribute("data-size", "md");
    expect(screen.getByRole("group", { name: "Второй уровень" })).toHaveAttribute("data-size", "sm");
  });

  it("вкладки: роли tab и tablist, значение у корня Tabs", async () => {
    const onValueChange = vi.fn();
    render(
      <Tabs value="a" onValueChange={onValueChange}>
        <Segment tabs label="Вкладки" options={[...options]} value="a" />
        <TabsContent value="a">А</TabsContent>
        <TabsContent value="b">Б</TabsContent>
      </Tabs>,
    );
    const list = screen.getByRole("tablist", { name: "Вкладки" });
    expect(list).toHaveClass("w-fit");
    expect(screen.getByRole("tab", { name: "Первый" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: "Первый" })).toHaveClass("h-8");
    await userEvent.setup().click(screen.getByRole("tab", { name: "Второй" }));
    expect(onValueChange).toHaveBeenCalledWith("b");
  });
});

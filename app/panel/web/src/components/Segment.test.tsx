import { render, screen } from "@testing-library/react";
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

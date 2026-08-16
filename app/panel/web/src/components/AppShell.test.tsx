import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AppShell } from "@/components/AppShell";
import { setCsrf } from "@/lib/api";

describe("AppShell header", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    setCsrf("");
  });

  it("shows backup actions, theme choices, and logout without account", async () => {
    const user = userEvent.setup();
    render(
      <AppShell onAddClient={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    await user.click(screen.getByRole("button", { name: "Бэкап" }));
    expect(await screen.findByText("Скачать")).toBeInTheDocument();
    expect(screen.getByText("Загрузить")).toBeInTheDocument();
    await user.keyboard("{Escape}");

    await user.click(screen.getByRole("button", { name: "Меню" }));
    expect(await screen.findByText("Тема")).toBeInTheDocument();
    expect(screen.queryByText("Светлая")).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Аккаунт" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Изменить пароль" })).not.toBeInTheDocument();
    expect(screen.queryByText("Изменить пароль")).not.toBeInTheDocument();
    expect(screen.getByText("Выйти")).toBeInTheDocument();

    await user.click(screen.getByText("Тема"));
    expect(await screen.findByText("Светлая")).toBeInTheDocument();
    expect(screen.getByText("Тёмная")).toBeInTheDocument();
    expect(screen.getByText("Системная")).toBeInTheDocument();
    expect(screen.getByText("AWG Panel")).toBeInTheDocument();
    expect(screen.queryByText("AmneziaVPN")).not.toBeInTheDocument();
  });

  it("shows Добавить клиента before backup", async () => {
    const user = userEvent.setup();
    const onAddClient = vi.fn();
    render(
      <AppShell onAddClient={onAddClient}>
        <p>body</p>
      </AppShell>,
    );

    const header = screen.getByRole("banner");
    const add = within(header).getByRole("button", { name: "Добавить клиента" });
    const backup = screen.getByRole("button", { name: "Бэкап" });
    const menu = screen.getByRole("button", { name: "Меню" });
    const headerButtons = [add, backup, menu];
    const all = within(header)
      .getAllByRole("button")
      .filter((el) => headerButtons.includes(el));
    expect(all).toEqual([add, backup, menu]);

    await user.click(add);
    expect(onAddClient).toHaveBeenCalledTimes(1);
  });

  it("hides the header Backup button below sm and shows it from sm up", () => {
    render(
      <AppShell onAddClient={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    expect(screen.getByRole("button", { name: "Бэкап" })).toHaveClass("hidden", "sm:inline-flex");
  });

  it("puts backup download and upload in the overflow menu", async () => {
    const user = userEvent.setup();
    render(
      <AppShell onAddClient={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    await user.click(screen.getByRole("button", { name: "Меню" }));
    const overflowBackup = await screen.findByRole("menuitem", { name: "Бэкап" });
    expect(overflowBackup).toHaveClass("sm:hidden");

    await user.click(overflowBackup);
    expect(await screen.findByRole("menuitem", { name: "Скачать" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Загрузить" })).toBeInTheDocument();
  });

  it("uses text-base on the AWG Panel title, not text-sm", () => {
    render(
      <AppShell onAddClient={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    const title = screen.getByText("AWG Panel");
    expect(title).toHaveClass("text-base");
    expect(title).not.toHaveClass("text-sm");
  });

  it("hides the header add on max-sm and keeps icon plus label from sm up", () => {
    render(
      <AppShell onAddClient={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    const headerAdd = within(screen.getByRole("banner")).getByRole("button", {
      name: "Добавить клиента",
    });
    expect(headerAdd).toHaveClass("hidden", "sm:inline-flex");
    expect(headerAdd.querySelector("svg")).toBeTruthy();
    const label = headerAdd.querySelector("span");
    expect(label).toHaveTextContent("Добавить клиента");
    expect(label).toHaveClass("sm:inline");
    expect(label).not.toHaveClass("hidden");
  });

  it("renders a max-sm FAB with gradient, 48px height, and icon plus text", async () => {
    const user = userEvent.setup();
    const onAddClient = vi.fn();
    render(
      <AppShell onAddClient={onAddClient}>
        <p>body</p>
      </AppShell>,
    );

    const headerAdd = within(screen.getByRole("banner")).getByRole("button", {
      name: "Добавить клиента",
    });
    const [first, second] = screen.getAllByRole("button", { name: "Добавить клиента" });
    const fabAdd = first === headerAdd ? second : first;
    expect(fabAdd).toBeDefined();
    expect(fabAdd).toHaveClass("h-12", "w-full");
    expect(fabAdd.querySelector("svg")).toBeTruthy();
    expect(fabAdd).toHaveTextContent("Добавить клиента");

    const fabBar = fabAdd.parentElement;
    expect(fabBar).toHaveClass("pb-6");
    expect(fabBar).toHaveClass("pointer-events-auto");

    const fabShell = fabAdd.closest(".fixed");
    expect(fabShell).toHaveClass("fixed", "inset-x-0", "bottom-0", "sm:hidden", "z-20");

    const gradient = fabShell?.querySelector(".bg-gradient-to-t");
    expect(gradient).toHaveClass("bg-gradient-to-t", "from-background", "to-transparent");
    expect(gradient).toHaveClass("pointer-events-none");

    await user.click(fabAdd);
    expect(onAddClient).toHaveBeenCalledTimes(1);
  });

  it("uses pt-4 on the header without py-4 bottom padding", () => {
    render(
      <AppShell onAddClient={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    const header = screen.getByRole("banner");
    expect(header).toHaveClass("pt-4");
    expect(header).not.toHaveClass("py-4");
  });

  it("uses outline variant on the overflow Меню button", () => {
    render(
      <AppShell onAddClient={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    const menu = screen.getByRole("button", { name: "Меню" });
    expect(menu).toHaveAttribute("data-variant", "outline");
    expect(menu).not.toHaveAttribute("data-variant", "ghost");
  });
});

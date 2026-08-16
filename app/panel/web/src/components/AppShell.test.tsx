import { render, screen } from "@testing-library/react";
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

  it("shows backup actions, theme choices, password and logout", async () => {
    const user = userEvent.setup();
    render(
      <AppShell totpEnabled={false} onTotpChange={() => {}} onAddClient={() => {}}>
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
    expect(screen.getByRole("menuitem", { name: "Аккаунт" })).toBeInTheDocument();
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
      <AppShell totpEnabled={false} onTotpChange={() => {}} onAddClient={onAddClient}>
        <p>body</p>
      </AppShell>,
    );

    const add = screen.getByRole("button", { name: "Добавить клиента" });
    const backup = screen.getByRole("button", { name: "Бэкап" });
    const menu = screen.getByRole("button", { name: "Меню" });
    const headerButtons = [add, backup, menu];
    const all = screen.getAllByRole("button").filter((el) => headerButtons.includes(el));
    expect(all).toEqual([add, backup, menu]);

    await user.click(add);
    expect(onAddClient).toHaveBeenCalledTimes(1);
  });

  it("hides the header Backup button below sm and shows it from sm up", () => {
    render(
      <AppShell totpEnabled={false} onTotpChange={() => {}} onAddClient={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    expect(screen.getByRole("button", { name: "Бэкап" })).toHaveClass("hidden", "sm:inline-flex");
  });

  it("puts backup download and upload in the overflow menu", async () => {
    const user = userEvent.setup();
    render(
      <AppShell totpEnabled={false} onTotpChange={() => {}} onAddClient={() => {}}>
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
      <AppShell totpEnabled={false} onTotpChange={() => {}} onAddClient={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    const title = screen.getByText("AWG Panel");
    expect(title).toHaveClass("text-base");
    expect(title).not.toHaveClass("text-sm");
  });

  it("keeps Добавить клиента as the accessible name and hides the label span below sm", () => {
    render(
      <AppShell totpEnabled={false} onTotpChange={() => {}} onAddClient={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    const add = screen.getByRole("button", { name: "Добавить клиента" });
    const label = add.querySelector("span");
    expect(label).toHaveClass("hidden", "sm:inline");
    expect(label).toHaveTextContent("Добавить клиента");
  });

  it("uses pt-4 on the header without py-4 bottom padding", () => {
    render(
      <AppShell totpEnabled={false} onTotpChange={() => {}} onAddClient={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    const header = screen.getByRole("banner");
    expect(header).toHaveClass("pt-4");
    expect(header).not.toHaveClass("py-4");
  });
});

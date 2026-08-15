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
      <AppShell totpEnabled={false} onTotpChange={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    await user.click(screen.getByRole("button", { name: "Бэкап" }));
    expect(await screen.findByText("Скачать")).toBeInTheDocument();
    expect(screen.getByText("Загрузить")).toBeInTheDocument();
    await user.keyboard("{Escape}");

    await user.click(screen.getByRole("button", { name: "Меню" }));
    expect(await screen.findByText("Светлая")).toBeInTheDocument();
    expect(screen.getByText("Тёмная")).toBeInTheDocument();
    expect(screen.getByText("Системная")).toBeInTheDocument();
    expect(screen.getByText("Изменить пароль")).toBeInTheDocument();
    expect(screen.getByText("Выйти")).toBeInTheDocument();
  });
});

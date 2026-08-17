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
    localStorage.clear();
    document.documentElement.classList.remove("dark");
  });

  it("has no overflow menu, logout, system theme, or account", async () => {
    const user = userEvent.setup();
    render(
      <AppShell onAddClient={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    expect(screen.queryByRole("button", { name: "Меню" })).not.toBeInTheDocument();
    expect(screen.queryByText("Выйти")).not.toBeInTheDocument();
    expect(screen.queryByText("Системная")).not.toBeInTheDocument();
    expect(screen.queryByText("Аккаунт")).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Аккаунт" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Бэкап" }));
    expect(await screen.findByText("Скачать")).toBeInTheDocument();
    expect(screen.getByText("Загрузить")).toBeInTheDocument();
    expect(screen.getByText("AWG Panel")).toBeInTheDocument();
    expect(screen.queryByText("AmneziaVPN")).not.toBeInTheDocument();
  });

  it("defaults to a dark theme toggle with a moon icon", () => {
    render(
      <AppShell onAddClient={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    const toggle = screen.getByRole("button", { name: "Тёмная тема" });
    expect(toggle).toHaveAttribute("data-variant", "outline");
    expect(toggle.querySelector("svg.lucide-moon")).toBeTruthy();
    expect(toggle.querySelector("svg.lucide-sun")).toBeNull();
  });

  it("toggles dark and light on click", async () => {
    const user = userEvent.setup();
    render(
      <AppShell onAddClient={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    const toggle = screen.getByRole("button", { name: "Тёмная тема" });
    await user.click(toggle);

    const light = screen.getByRole("button", { name: "Светлая тема" });
    expect(light.querySelector("svg.lucide-sun")).toBeTruthy();
    expect(document.documentElement.classList.contains("dark")).toBe(false);

    await user.click(light);
    const dark = screen.getByRole("button", { name: "Тёмная тема" });
    expect(dark.querySelector("svg.lucide-moon")).toBeTruthy();
    expect(document.documentElement.classList.contains("dark")).toBe(true);
  });

  it("shows Добавить клиента, then backup, then theme toggle", async () => {
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
    const theme = screen.getByRole("button", { name: "Тёмная тема" });
    const headerButtons = [add, backup, theme];
    const all = within(header)
      .getAllByRole("button")
      .filter((el) => headerButtons.includes(el));
    expect(all).toEqual([add, backup, theme]);
    expect(screen.queryByRole("button", { name: "Меню" })).not.toBeInTheDocument();

    await user.click(add);
    expect(onAddClient).toHaveBeenCalledTimes(1);
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
    expect(header).toHaveClass("sm:pt-8");
    expect(header).not.toHaveClass("pt-8");
    expect(header).not.toHaveClass("py-4");
  });

  it("renders one HeaderStats copy in the header row, not as a button", () => {
    render(
      <AppShell onAddClient={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    const header = screen.getByRole("banner");
    expect(header).toHaveClass("flex");
    expect(header).not.toHaveClass("flex-wrap");
    expect(header).toHaveClass("pt-4");
    expect(header).toHaveClass("sm:pt-8");
    expect(header).not.toHaveClass("pt-8");
    expect(header).not.toHaveClass("py-4");
    expect(header).not.toHaveClass("gap-2");

    const title = screen.getByText("AWG Panel");
    expect(title).toHaveClass("shrink-0");
    expect(title).not.toHaveClass("truncate");

    const cpus = within(header).getAllByLabelText("CPU");
    expect(cpus).toHaveLength(1);
    expect(cpus[0]).toHaveTextContent("\u2014");
    expect(cpus[0].textContent ?? "").not.toMatch(/cpu|ram|disk|CPU|RAM|Диск/);

    const stats = cpus[0].parentElement;
    expect(stats).toHaveClass("flex", "min-w-0", "ms-3");
    expect(stats).not.toHaveClass("hidden");
    expect(stats).not.toHaveClass("sm:flex");
    expect(stats).not.toHaveClass("sm:hidden");
    expect(stats).not.toHaveClass("w-full");

    expect(within(header).queryByRole("button", { name: /cpu/i })).not.toBeInTheDocument();
    expect(within(header).getByRole("button", { name: "Добавить клиента" })).toBeInTheDocument();
    expect(within(header).getByRole("button", { name: "Бэкап" })).toBeInTheDocument();
    expect(within(header).getByRole("button", { name: "Тёмная тема" })).toBeInTheDocument();
  });

  it("centers title only and omits stats and actions when empty", () => {
    render(
      <AppShell empty onAddClient={() => {}}>
        <p>body</p>
      </AppShell>,
    );

    const header = screen.getByRole("banner");
    expect(header).toHaveClass("flex", "min-w-0", "items-center", "justify-center");
    expect(header).not.toHaveClass("pt-4");
    expect(header).not.toHaveClass("sm:pt-8");
    const column = header.parentElement;
    expect(column).toHaveClass("flex", "min-h-svh", "flex-col", "items-center", "justify-center", "gap-8");
    expect(screen.getByText("AWG Panel")).toBeInTheDocument();
    expect(within(header).queryByLabelText("CPU")).toBeNull();
    expect(within(header).queryByLabelText("RAM")).toBeNull();
    expect(within(header).queryByLabelText("Диск")).toBeNull();

    expect(screen.queryByRole("button", { name: "Тёмная тема" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Светлая тема" })).not.toBeInTheDocument();
    expect(within(header).queryByRole("button", { name: "Добавить клиента" })).not.toBeInTheDocument();
    expect(within(header).queryByRole("button", { name: "Бэкап" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Добавить клиента" })).not.toBeInTheDocument();
  });
});


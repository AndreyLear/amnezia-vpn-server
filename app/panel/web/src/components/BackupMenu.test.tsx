import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { BackupMenu } from "@/components/BackupMenu";
import { setCsrf } from "@/lib/api";

const originalMatchMedia = window.matchMedia;

function stubMinWidthSm(matches: boolean) {
  window.matchMedia = vi.fn((query: string) => ({
    matches: query === "(min-width: 640px)" ? matches : false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }));
}

describe("BackupMenu close focus", () => {
  it("does not restore focus to the trigger after Escape closes the menu", async () => {
    const user = userEvent.setup();
    render(<BackupMenu />);

    const button = screen.getByRole("button", { name: "Бэкап" });
    await user.click(button);
    expect(button).toHaveAttribute("aria-expanded", "true");

    await user.keyboard("{Escape}");

    expect(button).toHaveAttribute("aria-expanded", "false");
    expect(button).not.toHaveFocus();
  });
});

describe("BackupMenu chevron", () => {
  it("keeps the backup trigger name and points the chevron down when closed", () => {
    render(<BackupMenu />);

    const button = screen.getByRole("button", { name: "Бэкап" });
    expect(button).not.toHaveClass("hidden");
    expect(button).not.toHaveClass("sm:inline-flex");
    expect(button.className.split(/\s+/)).toContain("inline-flex");
    expect(button).toHaveAttribute("aria-expanded", "false");

    const chevron = button.querySelector("svg");
    expect(chevron).not.toBeNull();
    expect(chevron).not.toHaveClass("rotate-180");
  });

  it("rotates the chevron when the backup menu is open", async () => {
    const user = userEvent.setup();
    render(<BackupMenu />);

    const button = screen.getByRole("button", { name: "Бэкап" });
    await user.click(button);

    expect(button).toHaveAttribute("aria-expanded", "true");
    const chevron = button.querySelector("svg");
    expect(chevron?.getAttribute("class") ?? "").toContain("rotate-180");
  });
});

describe("BackupMenu restore pending", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    setCsrf("");
  });

  it("opens the upload dialog without the restart sentence when restore is already pending", async () => {
    const user = userEvent.setup();
    render(<BackupMenu restorePending />);

    await user.click(screen.getByRole("button", { name: "Бэкап" }));
    await user.click(await screen.findByRole("menuitem", { name: "Загрузить" }));

    expect(await screen.findByRole("heading", { name: "Загрузить бэкап" })).toBeInTheDocument();
    expect(
      screen.queryByText("Восстановление уже подготовлено. Требуется перезапуск."),
    ).not.toBeInTheDocument();
    expect(document.querySelector('input[type="file"]')).toBeDisabled();
  });

  it("closes the upload dialog after a successful prepare", async () => {
    const user = userEvent.setup();
    setCsrf("csrf");
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        if (path.includes("/api/backups/restore")) {
          return new Response(
            JSON.stringify({
              ok: true,
              message: "Бэкап подготовлен. Требуется перезапуск.",
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          );
        }
        throw new Error(path);
      }),
    );

    render(<BackupMenu />);

    await user.click(screen.getByRole("button", { name: "Бэкап" }));
    await user.click(await screen.findByRole("menuitem", { name: "Загрузить" }));

    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    const file = new File(["archive"], "backup.tar.zst", { type: "application/octet-stream" });
    await user.upload(input, file);
    await user.click(screen.getByRole("button", { name: "Загрузить" }));

    await waitFor(() => {
      expect(screen.queryByRole("heading", { name: "Загрузить бэкап" })).not.toBeInTheDocument();
    });
    expect(
      screen.queryByText("Восстановление уже подготовлено. Требуется перезапуск."),
    ).not.toBeInTheDocument();
  });
});

describe("BackupMenu mobile sheet", () => {
  afterEach(() => {
    window.matchMedia = originalMatchMedia;
  });

  it("opens a bottom sheet with download and upload buttons instead of a menu", async () => {
    stubMinWidthSm(false);
    const user = userEvent.setup();
    render(<BackupMenu />);

    const trigger = screen.getByRole("button", { name: "Бэкап" });
    expect(trigger).not.toHaveAttribute("aria-haspopup", "menu");
    await user.click(trigger);

    expect(screen.getByRole("heading", { name: "Бэкап" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Скачать" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Загрузить" })).toBeInTheDocument();

    const content = document.querySelector('[data-slot="dialog-content"]');
    expect(content).toHaveClass("max-sm:bottom-0");
  });

  it("opens the upload dialog from the sheet without dropzone copy", async () => {
    stubMinWidthSm(false);
    const user = userEvent.setup();
    render(<BackupMenu />);

    await user.click(screen.getByRole("button", { name: "Бэкап" }));
    await user.click(screen.getByRole("button", { name: "Загрузить" }));

    expect(await screen.findByRole("heading", { name: "Загрузить бэкап" })).toBeInTheDocument();
    expect(
      screen.queryByText("Перетащите файл сюда или выберите на диске"),
    ).not.toBeInTheDocument();
    expect(document.querySelector('input[type="file"]')).toBeInTheDocument();
  });
});

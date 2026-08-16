import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { BackupMenu } from "@/components/BackupMenu";
import { setCsrf } from "@/lib/api";

describe("BackupMenu restore pending", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    setCsrf("");
  });

  it("opens the upload dialog with restart copy when restore is already pending", async () => {
    const user = userEvent.setup();
    render(<BackupMenu restorePending />);

    await user.click(screen.getByRole("button", { name: "Бэкап" }));
    await user.click(await screen.findByRole("menuitem", { name: "Загрузить" }));

    expect(
      await screen.findByText("Восстановление уже подготовлено. Требуется перезапуск."),
    ).toBeInTheDocument();
    expect(document.querySelector('input[type="file"]')).toBeDisabled();
  });

  it("keeps the restart copy visible after a successful prepare", async () => {
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

    expect(
      await screen.findByText("Восстановление уже подготовлено. Требуется перезапуск."),
    ).toBeInTheDocument();
    expect(document.querySelector('input[type="file"]')).toBeDisabled();
  });
});

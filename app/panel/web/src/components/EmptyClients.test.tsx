import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { BackupProvider } from "@/components/BackupMenu";
import { EmptyClients } from "@/components/EmptyClients";

describe("EmptyClients", () => {
  it("shows add and backup without caption or decorative cat", async () => {
    const user = userEvent.setup();
    const onAdd = vi.fn();
    render(
      <BackupProvider>
        <EmptyClients onAdd={onAdd} restorePending={false} />
      </BackupProvider>,
    );

    expect(screen.queryByText("Пока нет клиентов")).not.toBeInTheDocument();
    expect(document.querySelector("svg[aria-hidden='true'].size-28")).toBeNull();
    expect(
      [...document.querySelectorAll("style")]
        .map((el) => el.textContent ?? "")
        .join("\n"),
    ).not.toMatch(/empty-cat/);

    const add = screen.getByRole("button", { name: "Добавить клиента" });
    expect(add.querySelector("svg.lucide-plus")).toBeTruthy();
    const root = add.parentElement;
    expect(root).toHaveClass("flex", "flex-row", "items-center", "gap-2");
    expect(root).not.toHaveClass("gap-4");
    expect(root).not.toHaveClass("flex-col");
    expect(root?.className).not.toMatch(/min-h-\[calc\(100svh-8rem\)\]/);
    await user.click(add);
    expect(onAdd).toHaveBeenCalledTimes(1);

    expect(screen.queryByRole("button", { name: "Бэкап" })).not.toBeInTheDocument();

    const backup = screen.getByRole("button", { name: "Загрузить бэкап" });
    expect(backup.parentElement).toBe(root);
    expect(backup).toHaveAttribute("data-variant", "outline");
    expect(backup).not.toHaveAttribute("aria-expanded");
    await user.click(backup);
    expect(await screen.findByRole("heading", { name: "Загрузить бэкап" })).toBeInTheDocument();
    expect(screen.queryByText("Скачать")).not.toBeInTheDocument();
  });
});

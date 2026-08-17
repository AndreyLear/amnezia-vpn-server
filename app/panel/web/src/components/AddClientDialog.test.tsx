import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { AddClientDialog } from "@/components/AddClientDialog";

describe("AddClientDialog", () => {
  it("submits required name and optional description", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    render(
      <AddClientDialog open onOpenChange={() => {}} onSubmit={onSubmit} />,
    );

    expect(screen.getByText("(опционально)")).toBeInTheDocument();
    expect(screen.getByText("(опционально)")).toHaveClass("text-muted-foreground");

    await user.type(screen.getByLabelText("Имя"), "phone");
    await user.type(screen.getByLabelText(/Описание/), "office");
    await user.click(screen.getByRole("button", { name: "Добавить" }));

    expect(onSubmit).toHaveBeenCalledWith({
      name: "phone",
      description: "office",
    });
  });

  it("spaces fields 16px apart and 24px from title and footer", () => {
    render(
      <AddClientDialog open onOpenChange={() => {}} onSubmit={vi.fn()} />,
    );

    const content = document.querySelector("[data-slot=dialog-content]");
    expect(content).toHaveClass("gap-6");

    const form = content?.querySelector("form");
    expect(form).toHaveClass("grid", "gap-6");
    expect(form).not.toHaveClass("gap-3");

    const fieldStack = form?.querySelector(":scope > div");
    expect(fieldStack).toHaveClass("grid", "gap-4");
    expect(fieldStack?.querySelectorAll(":scope > div")).toHaveLength(2);
  });

  it("uses a growing textarea for the optional description", () => {
    render(
      <AddClientDialog open onOpenChange={() => {}} onSubmit={vi.fn()} />,
    );

    const description = screen.getByLabelText(/Описание/);
    expect(description.tagName).toBe("TEXTAREA");
    expect(description).toHaveClass("field-sizing-content", "min-h-8", "resize-none");
    expect(description).not.toHaveClass("h-8");
  });
});

import { render, screen, waitFor } from "@testing-library/react";
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

  it("opens as a mobile bottom sheet", () => {
    render(
      <AddClientDialog open onOpenChange={() => {}} onSubmit={vi.fn()} />,
    );

    const content = document.querySelector("[data-slot=dialog-content]");
    expect(content).toHaveClass("max-sm:bottom-0");
  });

  it("makes Добавить a 48px full-width tap target on mobile", () => {
    render(
      <AddClientDialog open onOpenChange={() => {}} onSubmit={vi.fn()} />,
    );

    expect(screen.getByRole("button", { name: "Добавить" })).toHaveClass(
      "max-sm:h-12",
      "max-sm:w-full",
    );
  });

  it("focuses Имя on open so the mobile keyboard can appear", async () => {
    render(
      <AddClientDialog open onOpenChange={() => {}} onSubmit={vi.fn()} />,
    );

    await waitFor(() => {
      expect(screen.getByLabelText("Имя")).toHaveFocus();
    });
    expect(document.getElementById("client-name")).toHaveFocus();
    expect(screen.getByRole("button", { name: "Close" })).not.toHaveFocus();
  });

  it("clears name and description when parent closes then reopens without onOpenChange", async () => {
    const user = userEvent.setup();
    const { rerender } = render(
      <AddClientDialog open onOpenChange={() => {}} onSubmit={vi.fn()} />,
    );

    await user.type(screen.getByLabelText("Имя"), "phone");
    await user.type(screen.getByLabelText(/Описание/), "office");

    rerender(
      <AddClientDialog open={false} onOpenChange={() => {}} onSubmit={vi.fn()} />,
    );
    rerender(
      <AddClientDialog open onOpenChange={() => {}} onSubmit={vi.fn()} />,
    );

    expect(screen.getByLabelText("Имя")).toHaveValue("");
    expect(screen.getByLabelText(/Описание/)).toHaveValue("");
  });

  it("clears fields after submit when parent closes then reopens", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const { rerender } = render(
      <AddClientDialog open onOpenChange={() => {}} onSubmit={onSubmit} />,
    );

    await user.type(screen.getByLabelText("Имя"), "phone");
    await user.type(screen.getByLabelText(/Описание/), "office");
    await user.click(screen.getByRole("button", { name: "Добавить" }));

    rerender(
      <AddClientDialog open={false} onOpenChange={() => {}} onSubmit={onSubmit} />,
    );
    rerender(
      <AddClientDialog open onOpenChange={() => {}} onSubmit={onSubmit} />,
    );

    expect(screen.getByLabelText("Имя")).toHaveValue("");
    expect(screen.getByLabelText(/Описание/)).toHaveValue("");
  });
});

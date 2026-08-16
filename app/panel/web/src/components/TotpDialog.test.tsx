import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { TotpDialog } from "@/components/TotpDialog";

function renderTotpDialog(onOpenChange = vi.fn()) {
  render(
    <TotpDialog
      open
      code=""
      error=""
      pending={false}
      onCodeChange={() => {}}
      onSubmit={() => {}}
      onOpenChange={onOpenChange}
    />,
  );
  return { onOpenChange };
}

describe("TotpDialog", () => {
  it("does not close when clicking the overlay", async () => {
    const user = userEvent.setup();
    const { onOpenChange } = renderTotpDialog();

    const overlay = document.querySelector('[data-slot="dialog-overlay"]');
    expect(overlay).not.toBeNull();

    await user.click(overlay!);

    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });

  it("closes when clicking the close button", async () => {
    const user = userEvent.setup();
    const { onOpenChange } = renderTotpDialog();

    await user.click(screen.getByRole("button", { name: "Close" }));

    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});

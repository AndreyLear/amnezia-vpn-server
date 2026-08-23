import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { BackupUploadDialog } from "@/components/BackupUploadDialog";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

const dropzoneCopy = "Перетащите файл сюда или выберите на диске";

function dropzone() {
  return screen.getByText(dropzoneCopy).closest("label")!;
}

function pickerInput(): HTMLInputElement {
  return document.querySelector('input[type="file"]')!;
}

describe("BackupUploadDialog file picker", () => {
  // The picker opens because the <input type=file> sits inside the <label>:
  // the browser activates it natively. Losing that nesting means the label
  // becomes decoration and only drag-and-drop is left.
  it("keeps the file input inside the label so the label activates it", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    expect(dropzone().contains(pickerInput())).toBe(true);
  });

  // Calling preventDefault on the label's own click cancels exactly that
  // native activation. Reaching for a scripted input.click() instead makes
  // the picker depend on how each browser treats a nested, script-issued
  // click during an already-handled event — Safari refuses it outright.
  it("does not cancel the label's native activation", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    const event = new MouseEvent("click", { bubbles: true, cancelable: true });
    dropzone().dispatchEvent(event);
    expect(event.defaultPrevented).toBe(false);
  });

  // display:none would stop several browsers from activating the control.
  it("hides the input accessibly rather than removing it from layout", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    expect(pickerInput().className).toContain("sr-only");
    expect(pickerInput().className).not.toContain("hidden");
  });
});

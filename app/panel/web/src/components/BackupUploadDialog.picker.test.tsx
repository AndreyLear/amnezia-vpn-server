import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { BackupUploadDialog } from "@/components/BackupUploadDialog";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

const dropzoneCopy = "Перетащите файл сюда или выберите на диске";

function dropzone(): HTMLElement {
  return screen.getByText(dropzoneCopy).closest('[role="button"]')! as HTMLElement;
}

function pickerInput(): HTMLInputElement {
  return document.querySelector('input[type="file"]')!;
}

describe("BackupUploadDialog file picker", () => {
  // Not a <label> with the input inside: Safari refused the scripted click
  // the label handler issued, and Helium never activated the hidden input
  // from the label at all. A clickable block plus click() on an input beside
  // it is the arrangement that works in both.
  it("does not wrap the dropzone in a label", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    expect(dropzone().tagName).not.toBe("LABEL");
    expect(dropzone().closest("label")).toBeNull();
  });

  it("keeps the file input outside the clickable area", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    expect(dropzone().contains(pickerInput())).toBe(false);
  });

  it("opens the picker from the click handler", async () => {
    const user = userEvent.setup();
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    const activated = vi.fn();
    pickerInput().addEventListener("click", activated);

    await user.click(dropzone());

    expect(activated).toHaveBeenCalled();
  });

  // Keyboard users reach it too, since a div is not focusable on its own.
  it("opens the picker from the keyboard", async () => {
    const user = userEvent.setup();
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    const activated = vi.fn();
    pickerInput().addEventListener("click", activated);

    dropzone().focus();
    await user.keyboard("{Enter}");

    expect(activated).toHaveBeenCalled();
  });

  // display:none would stop several browsers from activating the control.
  it("hides the input accessibly rather than removing it from layout", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    expect(pickerInput().className).toContain("sr-only");
    expect(pickerInput().className).not.toContain("hidden");
  });
});

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { BackupUploadDialog } from "@/components/BackupUploadDialog";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

const dropzoneCopy = "Перетащите файл сюда или выберите на диске";

function dropzone(): HTMLElement {
  return screen.getByTestId("dropzone");
}

function pickerInput(): HTMLInputElement {
  return document.querySelector('input[type="file"]')!;
}

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
  })) as unknown as typeof window.matchMedia;
}

describe("BackupUploadDialog file picker", () => {
  beforeEach(() => stubMinWidthSm(true));

  // Neither a <label> around a hidden input (Helium never activated it) nor a
  // scripted input.click() (Safari refused it inside the label's own click,
  // and Helium refuses it outright) can be relied on. The input itself covers
  // the dropzone at zero opacity, so the click lands on a plain file input and
  // the browser opens its own dialog with no script in between.
  it("puts the real input over the dropzone", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    const input = pickerInput();
    expect(dropzone().contains(input)).toBe(true);
    expect(input.className).toContain("absolute");
    expect(input.className).toContain("inset-0");
    expect(input.className).toContain("opacity-0");
  });

  it("does not wrap the dropzone in a label", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    expect(dropzone().closest("label")).toBeNull();
  });

  // Nothing may call click() on it: that is the call Helium refuses.
  it("never opens the picker through a scripted click on the desktop", async () => {
    const user = userEvent.setup();
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    const scripted = vi.spyOn(HTMLInputElement.prototype, "click");

    await user.click(pickerInput());

    expect(scripted).not.toHaveBeenCalled();
    scripted.mockRestore();
  });

  // On the desktop the input is the visible target itself; on a phone it stays
  // hidden behind a button. Neither may use display:none, which stops several
  // browsers from activating the control at all.
  it("never removes the input from layout", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    expect(pickerInput().className).not.toContain("hidden");
  });
});

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { BackupUploadDialog } from "@/components/BackupUploadDialog";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

const overlayText = "Отпустите файл";

function dragFileOverWindow() {
  fireEvent.dragOver(document, {
    dataTransfer: { files: [new File(["a"], "backup.tar.zst")], types: ["Files"] },
  });
}

describe("BackupUploadDialog window-wide drop target", () => {
  // Dropping already works anywhere in the window, but only the small dashed
  // box lit up — so the window did not look like a target and people aimed
  // for the rectangle.
  it("takes over the whole window while a file is dragged", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    expect(screen.queryByText(overlayText)).not.toBeInTheDocument();

    dragFileOverWindow();

    // the outer element is the overlay; the inner one is the dashed card
    const overlay = screen.getByText(overlayText).closest('[aria-hidden="true"]');
    expect(overlay).toBeTruthy();
    expect(overlay?.className).toContain("fixed");
    expect(overlay?.className).toContain("inset-0");
  });

  it("gives the window back when the drag leaves", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    dragFileOverWindow();
    expect(screen.getByText(overlayText)).toBeInTheDocument();

    fireEvent.dragEnd(document);
    expect(screen.queryByText(overlayText)).not.toBeInTheDocument();
  });

  it("clears the overlay once the file is dropped", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    dragFileOverWindow();
    fireEvent.drop(document, {
      dataTransfer: { files: [new File(["a"], "backup.tar.zst")] },
    });
    expect(screen.queryByText(overlayText)).not.toBeInTheDocument();
  });
});

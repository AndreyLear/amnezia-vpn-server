import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { BackupUploadDialog } from "@/components/BackupUploadDialog";

describe("BackupUploadDialog drop", () => {
  it("accepts a dropped file", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    const dropzone = screen.getByText("Перетащите файл сюда или выберите на диске").closest("label");
    expect(dropzone).toBeTruthy();
    const file = new File(["archive"], "panel.zst");
    fireEvent.drop(dropzone!, {
      dataTransfer: { files: [file] },
    });
    expect(screen.getByText("panel.zst")).toBeInTheDocument();
  });

  it("accepts a file dropped on the overlay without closing", () => {
    const onOpenChange = vi.fn();
    render(<BackupUploadDialog open onOpenChange={onOpenChange} />);
    const overlay = document.querySelector("[data-slot=dialog-overlay]");
    expect(overlay).toBeTruthy();
    const file = new File(["archive"], "overlay.zst");
    const dragOver = fireEvent.dragOver(overlay!, {
      dataTransfer: { files: [file] },
    });
    expect(dragOver).toBe(false);
    const label = screen.getByText("Перетащите файл сюда или выберите на диске").closest("label");
    expect(label?.className).toContain("border-primary");
    fireEvent.drop(overlay!, {
      dataTransfer: { files: [file] },
    });
    expect(screen.getByText("overlay.zst")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Загрузить" })).toBeEnabled();
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });
});

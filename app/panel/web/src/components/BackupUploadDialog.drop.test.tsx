import { fireEvent, render, screen } from "@testing-library/react";
import { toast } from "sonner";
import { afterEach, describe, expect, it, vi } from "vitest";

import { BackupUploadDialog } from "@/components/BackupUploadDialog";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

const rejectCopy = "Нужен файл бэкапа (.tar.zst).";
const archiveName = "backup-2026-08-16.tar.zst";

describe("BackupUploadDialog drop", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("uses a brighter hover border on the idle dashed dropzone", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    const dropzone = screen.getByText("Перетащите файл сюда или выберите на диске").closest("label");
    expect(dropzone?.className).toContain("border-border");
    expect(dropzone?.className).toContain("hover:border-input");
  });

  it("keeps a primary border while dragging over the dashed dropzone", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    const dropzone = screen.getByText("Перетащите файл сюда или выберите на диске").closest("label");
    fireEvent.dragOver(dropzone!, {
      dataTransfer: { files: [new File(["archive"], archiveName)] },
    });
    expect(dropzone?.className).toContain("border-primary");
  });

  it("accepts a dropped backup archive", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    const dropzone = screen.getByText("Перетащите файл сюда или выберите на диске").closest("label");
    expect(dropzone).toBeTruthy();
    const file = new File(["archive"], archiveName);
    fireEvent.drop(dropzone!, {
      dataTransfer: { files: [file] },
    });
    expect(screen.getByText(archiveName)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Загрузить" })).toBeEnabled();
  });

  it("rejects notes.txt dropped on the dashed box", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    const dropzone = screen.getByText("Перетащите файл сюда или выберите на диске").closest("label");
    fireEvent.drop(dropzone!, {
      dataTransfer: { files: [new File(["x"], "notes.txt")] },
    });
    expect(screen.queryByText("notes.txt")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Загрузить" })).toBeDisabled();
    expect(toast.error).toHaveBeenCalledTimes(1);
    expect(toast.error).toHaveBeenCalledWith(rejectCopy);
  });

  it("rejects panel.zst dropped on the overlay", () => {
    const onOpenChange = vi.fn();
    render(<BackupUploadDialog open onOpenChange={onOpenChange} />);
    const overlay = document.querySelector("[data-slot=dialog-overlay]");
    expect(overlay).toBeTruthy();
    fireEvent.drop(overlay!, {
      dataTransfer: { files: [new File(["archive"], "panel.zst")] },
    });
    expect(screen.queryByText("panel.zst")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Загрузить" })).toBeDisabled();
    expect(toast.error).toHaveBeenCalledWith(rejectCopy);
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });

  it("accepts a backup archive dropped on the overlay without closing", () => {
    const onOpenChange = vi.fn();
    render(<BackupUploadDialog open onOpenChange={onOpenChange} />);
    const overlay = document.querySelector("[data-slot=dialog-overlay]");
    expect(overlay).toBeTruthy();
    const file = new File(["archive"], archiveName);
    const dragOver = fireEvent.dragOver(overlay!, {
      dataTransfer: { files: [file] },
    });
    expect(dragOver).toBe(false);
    const label = screen.getByText("Перетащите файл сюда или выберите на диске").closest("label");
    expect(label?.className).toContain("border-primary");
    fireEvent.drop(overlay!, {
      dataTransfer: { files: [file] },
    });
    expect(screen.getByText(archiveName)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Загрузить" })).toBeEnabled();
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });
});

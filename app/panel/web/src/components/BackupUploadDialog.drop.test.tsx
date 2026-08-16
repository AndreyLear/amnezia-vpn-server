import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

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
});

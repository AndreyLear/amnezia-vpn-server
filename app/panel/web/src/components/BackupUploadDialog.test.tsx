import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { BackupUploadDialog } from "@/components/BackupUploadDialog";

describe("BackupUploadDialog", () => {
  it("accepts a file from the picker with .tar.zst,.zst", async () => {
    const user = userEvent.setup();
    render(<BackupUploadDialog open onOpenChange={() => {}} />);

    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    expect(input).toHaveAttribute("accept", ".tar.zst,.zst");

    const file = new File(["archive"], "backup.tar.zst", { type: "application/octet-stream" });
    await user.upload(input, file);
    expect(screen.getByText("backup.tar.zst")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Загрузить" })).toBeEnabled();
  });
});

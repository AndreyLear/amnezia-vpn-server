import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { afterEach, describe, expect, it, vi } from "vitest";

import { BackupUploadDialog } from "@/components/BackupUploadDialog";
import { setCsrf } from "@/lib/api";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

const rejectCopy = "Нужен файл бэкапа (.tar.zst).";

describe("BackupUploadDialog", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    setCsrf("");
  });

  it("accepts a file from the picker with .tar.zst only", async () => {
    const user = userEvent.setup();
    render(<BackupUploadDialog open onOpenChange={() => {}} />);

    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    expect(input).toHaveAttribute("accept", ".tar.zst");

    const file = new File(["archive"], "backup-2026-08-16.tar.zst", {
      type: "application/octet-stream",
    });
    await user.upload(input, file);
    expect(screen.getByText("backup-2026-08-16.tar.zst")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Загрузить" })).toBeEnabled();
  });

  it("truncates the selected filename and keeps the dialog from growing", async () => {
    const user = userEvent.setup();
    render(<BackupUploadDialog open onOpenChange={() => {}} />);

    const content = document.querySelector("[data-slot=dialog-content]");
    expect(content?.className).toContain("sm:max-w-md");
    expect(content?.className).toContain("overflow-hidden");

    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    const file = new File(["archive"], "backup.tar.zst", { type: "application/octet-stream" });
    await user.upload(input, file);

    const filename = screen.getByText("backup.tar.zst");
    expect(filename).toBeInTheDocument();
    expect(filename.className).toContain("truncate");
    expect(filename.className).toContain("min-w-0");
  });

  it("rejects notes.txt and photo.png from the picker", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;

    for (const name of ["notes.txt", "photo.png"]) {
      vi.mocked(toast.error).mockClear();
      fireEvent.change(input, {
        target: { files: [new File(["x"], name, { type: "application/octet-stream" })] },
      });
      expect(screen.queryByText(name)).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Загрузить" })).toBeDisabled();
      expect(toast.error).toHaveBeenCalledWith(rejectCopy);
    }
  });

  it("disables upload and shows restart copy when restore is pending", () => {
    render(<BackupUploadDialog open restorePending onOpenChange={() => {}} />);

    expect(screen.getByText("Восстановление уже подготовлено. Требуется перезапуск.")).toBeInTheDocument();
    expect(document.querySelector('input[type="file"]')).toBeDisabled();
    expect(screen.getByRole("button", { name: "Загрузить" })).toBeDisabled();
  });

  it("shows a spinner on Загрузить while POST /api/backups/restore is pending", async () => {
    const user = userEvent.setup();
    setCsrf("csrf");
    let resolveFetch!: (value: Response) => void;
    const deferred = new Promise<Response>((resolve) => {
      resolveFetch = resolve;
    });
    vi.stubGlobal(
      "fetch",
      vi.fn(() => deferred),
    );

    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    const file = new File(["archive"], "backup-2026-08-16.tar.zst", {
      type: "application/octet-stream",
    });
    await user.upload(input, file);
    await user.click(screen.getByRole("button", { name: "Загрузить" }));

    const submit = await screen.findByRole("button", { name: /Загрузить/ });
    expect(submit).toBeDisabled();
    expect(submit).toHaveTextContent("Загрузить");
    expect(submit.querySelector('[data-slot="spinner"]')).toBeTruthy();

    resolveFetch(
      new Response(JSON.stringify({ ok: true, message: "ok" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    await waitFor(() => {
      const settled = screen.getByRole("button", { name: "Загрузить" });
      expect(settled.querySelector('[data-slot="spinner"]')).toBeNull();
    });
  });
});

import { useState } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { afterEach, describe, expect, it, vi } from "vitest";

import { BackupUploadDialog, DROPZONE_MISS_MS } from "@/components/BackupUploadDialog";
import { setCsrf } from "@/lib/api";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

const rejectCopy = "Нужен файл бэкапа (.tar.zst).";
const originalMatchMedia = window.matchMedia;

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
  }));
}

describe("BackupUploadDialog", () => {
  afterEach(() => {
    window.matchMedia = originalMatchMedia;
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
    expect(content).toHaveClass("gap-6");
    const form = content?.querySelector("form");
    expect(form).toHaveClass("grid", "gap-6");
    expect(form).not.toHaveClass("gap-3");

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
      expect(screen.getByRole("button", { name: "Загрузить" })).toBeEnabled();
      expect(toast.error).toHaveBeenCalledWith(rejectCopy);
    }
  });

  it("does not show the restart sentence even if restore is pending", () => {
    render(<BackupUploadDialog open restorePending onOpenChange={() => {}} />);

    expect(
      screen.queryByText("Восстановление уже подготовлено. Требуется перезапуск."),
    ).not.toBeInTheDocument();
    expect(document.querySelector('input[type="file"]')).toBeDisabled();
    expect(screen.getByRole("button", { name: "Загрузить" })).toBeDisabled();
  });

  it("closes the dialog after a successful restore", async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    setCsrf("csrf");
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ ok: true, message: "ok" }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
      ),
    );

    function Harness() {
      const [open, setOpen] = useState(true);
      return (
        <BackupUploadDialog
          open={open}
          onOpenChange={(next) => {
            onOpenChange(next);
            setOpen(next);
          }}
        />
      );
    }

    render(<Harness />);
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    const file = new File(["archive"], "backup.tar.zst", { type: "application/octet-stream" });
    await user.upload(input, file);
    await user.click(screen.getByRole("button", { name: "Загрузить" }));

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });
    expect(screen.queryByRole("heading", { name: "Загрузить бэкап" })).not.toBeInTheDocument();
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

  it("keeps Загрузить enabled when the dialog is idle with no file", () => {
    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    expect(screen.getByRole("button", { name: "Загрузить" })).toBeEnabled();
  });

  it("highlights the dashed dropzone when Загрузить is pressed with no file", () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    try {
      const fetchMock = vi.fn();
      vi.stubGlobal("fetch", fetchMock);
      vi.mocked(toast.error).mockClear();
      vi.mocked(toast.success).mockClear();

      render(<BackupUploadDialog open onOpenChange={() => {}} />);
      fireEvent.click(screen.getByRole("button", { name: "Загрузить" }));

      const dropzone = screen
        .getByText("Перетащите файл сюда или выберите на диске")
        .closest("label");
      expect(DROPZONE_MISS_MS).toBe(1500);
      expect(dropzone?.className.split(/\s+/)).toContain("border-destructive");
      expect(dropzone?.className.split(/\s+/)).toContain("duration-500");
      expect(fetchMock).not.toHaveBeenCalled();
      expect(toast.error).not.toHaveBeenCalled();
      expect(toast.success).not.toHaveBeenCalled();

      act(() => {
        vi.advanceTimersByTime(DROPZONE_MISS_MS);
      });
      expect(dropzone?.className.split(/\s+/)).not.toContain("border-destructive");
    } finally {
      vi.useRealTimers();
    }
  });

  it("highlights the mobile picker when Загрузить is pressed with no file", () => {
    stubMinWidthSm(false);
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    try {
      render(<BackupUploadDialog open onOpenChange={() => {}} />);
      fireEvent.click(screen.getByRole("button", { name: "Загрузить" }));

      const picker = screen.getByRole("button", { name: "Выберите файл на устройстве" });
      expect(picker.className.split(/\s+/)).toContain("border-destructive");
      expect(picker.className.split(/\s+/)).toContain("duration-500");
    } finally {
      vi.useRealTimers();
    }
  });

  it("uses a device picker without dropzone copy on mobile", () => {
    stubMinWidthSm(false);
    render(<BackupUploadDialog open onOpenChange={() => {}} />);

    expect(
      screen.queryByText("Перетащите файл сюда или выберите на диске"),
    ).not.toBeInTheDocument();
    expect(screen.getByText("Выберите файл на устройстве")).toBeInTheDocument();
    expect(document.querySelector('input[type="file"]')).toHaveAttribute(
      "accept",
      ".tar.zst",
    );
  });

  // The picker must open through the label's own activation of the nested
  // input, not through a scripted input.click(): browsers treat a
  // script-issued click during an already-handled event inconsistently and
  // Safari refuses it, which is how the picker stopped opening at all.
  it("opens the file picker when the desktop dropzone is clicked", async () => {
    const user = userEvent.setup();
    render(<BackupUploadDialog open onOpenChange={() => {}} />);

    const input = document.querySelector('input[type="file"]')!;
    const activated = vi.fn();
    input.addEventListener("click", activated);

    const dropzone = screen
      .getByText("Перетащите файл сюда или выберите на диске")
      .closest("label");
    await user.click(dropzone!);

    expect(activated).toHaveBeenCalled();
  });

  it("does not look clickable when restore is pending", async () => {
    const user = userEvent.setup();
    const clickSpy = vi.spyOn(HTMLInputElement.prototype, "click");
    render(<BackupUploadDialog open restorePending onOpenChange={() => {}} />);

    const dropzone = screen
      .getByText("Перетащите файл сюда или выберите на диске")
      .closest("label");
    expect(dropzone?.className.split(/\s+/)).not.toContain("cursor-pointer");
    expect(dropzone?.className.split(/\s+/)).toContain("cursor-not-allowed");

    await user.click(dropzone!);
    expect(clickSpy).not.toHaveBeenCalled();
  });
});

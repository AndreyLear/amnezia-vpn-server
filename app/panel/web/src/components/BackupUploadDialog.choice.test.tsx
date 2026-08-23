import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { BackupUploadDialog } from "@/components/BackupUploadDialog";
import { setCsrf } from "@/lib/api";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

const needsChoice = {
  ok: false,
  needs_choice: true,
  message: "Бэкап снят на другом сервере: выберите, какой адрес использовать",
  archive_endpoint: "old.example.com:443",
  server_endpoint: "2.26.93.192:443",
  archive_mtu: "1380",
  server_mtu: "1416",
};

function jsonResponse(body: unknown, status: number) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

async function uploadArchive(user: ReturnType<typeof userEvent.setup>) {
  const input = document.querySelector('input[type="file"]') as HTMLInputElement;
  await user.upload(input, new File(["archive"], "backup.tar.zst"));
  await user.click(screen.getByRole("button", { name: "Загрузить" }));
}

describe("BackupUploadDialog address choice", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  // Migration is the reason this product exists: install fresh, upload the
  // backup. The archive carries the old server's address, so the dialog has
  // to ask rather than quietly point every client at the machine the
  // operator just left.
  it("asks which address to use when the archive came from another server", async () => {
    const user = userEvent.setup();
    setCsrf("csrf");
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse(needsChoice, 409)));

    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    await uploadArchive(user);

    expect(await screen.findByText(/old\.example\.com:443/)).toBeInTheDocument();
    expect(screen.getByText(/2\.26\.93\.192:443/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Оставить адрес из бэкапа/ })).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Использовать адрес этого сервера/ }),
    ).toBeInTheDocument();
  });

  it("resends the same archive with the chosen address", async () => {
    const user = userEvent.setup();
    setCsrf("csrf");
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(needsChoice, 409))
      .mockResolvedValueOnce(jsonResponse({ ok: true, message: "Восстановление применено" }, 200));
    vi.stubGlobal("fetch", fetchMock);

    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    await uploadArchive(user);
    await user.click(await screen.findByRole("button", { name: /Использовать адрес этого сервера/ }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(2);
    });
    const body = fetchMock.mock.calls[1][1].body as FormData;
    expect(body.get("endpoint")).toBe("server");
    expect(body.get("backup")).toBeInstanceOf(File);
  });

  it("sends the archive's own address when that is chosen", async () => {
    const user = userEvent.setup();
    setCsrf("csrf");
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(needsChoice, 409))
      .mockResolvedValueOnce(jsonResponse({ ok: true, message: "Восстановление применено" }, 200));
    vi.stubGlobal("fetch", fetchMock);

    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    await uploadArchive(user);
    await user.click(await screen.findByRole("button", { name: /Оставить адрес из бэкапа/ }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(2);
    });
    expect((fetchMock.mock.calls[1][1].body as FormData).get("endpoint")).toBe("archive");
  });

  // Choosing this server's address invalidates configs that clients already
  // have; saying so is the difference between a working migration and a
  // silent one.
  it("warns that client configs must be reissued for the server address", async () => {
    const user = userEvent.setup();
    setCsrf("csrf");
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse(needsChoice, 409)));

    render(<BackupUploadDialog open onOpenChange={() => {}} />);
    await uploadArchive(user);

    expect(await screen.findByText(/перевыпустить/i)).toBeInTheDocument();
  });
});

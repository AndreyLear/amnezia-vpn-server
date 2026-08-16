import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ClientInfoDialog } from "@/components/ClientInfoDialog";
import type { Client } from "@/lib/api";

const client: Client = {
  id: 7,
  name: "Alice",
  description: "phone",
  address: "10.8.0.2/32",
  enabled: true,
  online: false,
  last_handshake_utc: null,
  rx_bytes: 0,
  tx_bytes: 0,
};

describe("ClientInfoDialog", () => {
  it("uses a fixed dialog title", () => {
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    expect(screen.getByRole("heading", { name: "Клиент" })).toBeInTheDocument();
  });

  it("shows client parameters as a plain definition list", () => {
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    expect(screen.getByText("Статус")).toBeInTheDocument();
    expect(screen.getByText("офлайн")).toBeInTheDocument();
    expect(screen.getByText("IP")).toBeInTheDocument();
    expect(screen.getByText("Handshake")).toBeInTheDocument();
    expect(screen.getByText("Трафик")).toBeInTheDocument();
    expect(document.querySelector('[data-slot="badge"]')).toBeNull();
  });

  it("keeps handshake as a UTC timestamp", () => {
    render(
      <ClientInfoDialog
        client={{ ...client, last_handshake_utc: "2026-08-16T00:00:00Z" }}
        onOpenChange={() => {}}
      />,
    );

    expect(screen.getByText("2026-08-16 00:00:00 UTC")).toBeInTheDocument();
  });

  it("shows action buttons without the client menu", () => {
    render(
      <ClientInfoDialog
        client={client}
        onOpenChange={() => {}}
        onQr={() => {}}
        onDownload={() => {}}
        onToggle={() => {}}
        onDelete={() => {}}
      />,
    );

    expect(screen.getByRole("button", { name: "QR-код" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Скачать конфиг" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Отключить" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Удалить" })).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: `Действия для ${client.name}` }),
    ).not.toBeInTheDocument();
    expect(
      document.querySelector('[data-slot="dropdown-menu-trigger"]'),
    ).toBeNull();
  });

  it("closes without saving when name and description are unchanged", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn();
    const onOpenChange = vi.fn();

    render(
      <ClientInfoDialog
        client={client}
        onOpenChange={onOpenChange}
        onSave={onSave}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Сохранить" }));

    expect(onSave).not.toHaveBeenCalled();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("saves and closes when name changes", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(true);
    const onOpenChange = vi.fn();

    render(
      <ClientInfoDialog
        client={client}
        onOpenChange={onOpenChange}
        onSave={onSave}
      />,
    );

    const name = screen.getByLabelText("Имя");
    await user.clear(name);
    await user.type(name, "Bob");
    await user.click(screen.getByRole("button", { name: "Сохранить" }));

    expect(onSave).toHaveBeenCalledWith({ name: "Bob", description: "phone" });
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});

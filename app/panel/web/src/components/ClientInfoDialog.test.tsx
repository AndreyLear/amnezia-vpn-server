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
  it("does not render a name input when opened", () => {
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    expect(screen.queryByLabelText("Имя")).toBeNull();
    expect(document.querySelector("#info-name")).toBeNull();
    expect(screen.getByRole("heading", { name: "Клиент" })).toBeInTheDocument();
  });

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

  it("keeps handshake as a timestamp in Russian writing", () => {
    render(
      <ClientInfoDialog
        client={{ ...client, last_handshake_utc: "2026-08-16T00:00:00Z" }}
        onOpenChange={() => {}}
      />,
    );

    expect(screen.getByText("16.08.2026, 00:00:00")).toBeInTheDocument();
  });

  it("shows paused status and enable button when the client is disabled", () => {
    render(
      <ClientInfoDialog
        client={{ ...client, enabled: false, online: true }}
        onOpenChange={() => {}}
        onToggle={() => {}}
      />,
    );

    const status = screen.getByText("Статус").closest("div");
    expect(status).toHaveTextContent("пауза");
    expect(status).not.toHaveTextContent("онлайн");
    expect(status).not.toHaveTextContent("офлайн");
    expect(screen.getByRole("button", { name: "Включить" })).toBeInTheDocument();
    expect(document.querySelector('[data-slot="badge"]')).toBeNull();
  });

  it("shows online status and disable button when the client is enabled and online", () => {
    render(
      <ClientInfoDialog
        client={{ ...client, enabled: true, online: true }}
        onOpenChange={() => {}}
        onToggle={() => {}}
      />,
    );

    expect(screen.getByText("онлайн")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Отключить" })).toBeInTheDocument();
    expect(document.querySelector('[data-slot="badge"]')).toBeNull();
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

  it("shows name and description as read-only text with Изменить buttons", () => {
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("phone")).toBeInTheDocument();
    expect(screen.getByText("(опционально)")).toHaveClass("text-muted-foreground");

    const editName = screen.getByRole("button", { name: "Изменить имя" });
    const editDescription = screen.getByRole("button", { name: "Изменить описание" });
    expect(editName).toHaveTextContent("Изменить");
    expect(editDescription).toHaveTextContent("Изменить");

    expect(document.querySelector("#info-name")).toBeNull();
    expect(document.querySelector("#info-description")).toBeNull();
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByRole("button", { name: "Сохранить" })).toBeNull();
  });

  it("cancels name edit without calling onSave", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn();

    render(
      <ClientInfoDialog
        client={client}
        onOpenChange={() => {}}
        onSave={onSave}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Изменить имя" }));

    const name = screen.getByLabelText("Имя");
    expect(name).toHaveValue("Alice");
    await user.clear(name);
    await user.type(name, "Bob");
    await user.click(screen.getByRole("button", { name: "Отменить имя" }));

    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(document.querySelector("#info-name")).toBeNull();
    expect(onSave).not.toHaveBeenCalled();
  });

  it("saves a changed name without closing the dialog", async () => {
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

    await user.click(screen.getByRole("button", { name: "Изменить имя" }));
    const name = screen.getByLabelText("Имя");
    await user.clear(name);
    await user.type(name, "Bob");
    await user.click(screen.getByRole("button", { name: "Сохранить имя" }));

    expect(onSave).toHaveBeenCalledWith({ name: "Bob", description: "phone" });
    expect(onOpenChange).not.toHaveBeenCalled();
    expect(document.querySelector("#info-name")).toBeNull();
    expect(screen.getByText("Bob")).toBeInTheDocument();
  });

  it("edits description in a growing textarea and saves without closing", async () => {
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

    await user.click(screen.getByRole("button", { name: "Изменить описание" }));

    const description = screen.getByLabelText(/Описание/);
    expect(description.tagName).toBe("TEXTAREA");
    expect(description).toHaveClass("field-sizing-content", "min-h-8", "resize-none");
    expect(description).not.toHaveClass("h-8");

    await user.click(description);
    await user.type(description, "{Enter}more");

    expect(description).toHaveValue("phone\nmore");
    expect(onSave).not.toHaveBeenCalled();
    expect(onOpenChange).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Сохранить описание" }));

    expect(onSave).toHaveBeenCalledWith({ name: "Alice", description: "phone\nmore" });
    expect(onOpenChange).not.toHaveBeenCalled();
    expect(document.querySelector("#info-description")).toBeNull();
  });

  it("exits name edit without onSave when the value is unchanged", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn();

    render(
      <ClientInfoDialog
        client={client}
        onOpenChange={() => {}}
        onSave={onSave}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Изменить имя" }));
    await user.click(screen.getByRole("button", { name: "Сохранить имя" }));

    expect(onSave).not.toHaveBeenCalled();
    expect(document.querySelector("#info-name")).toBeNull();
    expect(screen.getByText("Alice")).toBeInTheDocument();
  });
});

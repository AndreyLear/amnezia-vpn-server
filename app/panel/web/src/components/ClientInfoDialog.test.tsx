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

  it("adds 4px more space between the properties list and the action buttons", () => {
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

    const qr = screen.getByRole("button", { name: "QR-код" });
    const buttonRow = qr.parentElement;
    expect(buttonRow).toHaveClass("flex", "flex-wrap", "gap-2", "max-sm:flex-col");
    expect(qr).toHaveClass("max-sm:h-12", "max-sm:w-full");
    expect(buttonRow?.parentElement).toHaveClass("gap-4");
  });

  it("opens as a mobile bottom sheet with property dividers", () => {
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    const content = document.querySelector('[data-slot="dialog-content"]');
    expect(content).toHaveClass(
      "max-sm:bottom-0",
      "max-sm:top-auto",
      "max-sm:left-0",
      "max-sm:right-0",
      "max-sm:w-full",
      "max-sm:max-w-none",
      "max-sm:translate-x-0",
      "max-sm:translate-y-0",
      "max-sm:rounded-b-none",
      "h-auto",
      "overflow-y-auto",
      "max-h-[calc(100dvh-2rem)]",
      "sm:max-w-md",
    );

    const dl = document.querySelector("dl");
    expect(dl).toHaveClass("max-sm:divide-y", "max-sm:divide-border");
  });

  it("vertically centers Изменить against the whole name property", () => {
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    const editName = screen.getByRole("button", { name: "Изменить имя" });
    const row = editName.parentElement;
    expect(row).toHaveClass("flex", "items-center");
    expect(row).not.toHaveClass("justify-between");
    expect(row).toContainElement(screen.getByText("Имя"));
    expect(row).toContainElement(screen.getByText("Alice"));
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

  it("keeps Save and Cancel flush without a flex gap", async () => {
    const user = userEvent.setup();

    render(
      <ClientInfoDialog
        client={client}
        onOpenChange={() => {}}
        onSave={vi.fn()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Изменить имя" }));

    const save = screen.getByRole("button", { name: "Сохранить имя" });
    expect(save.parentElement).toHaveClass("flex");
    expect(save.parentElement).not.toHaveClass("gap-1");
  });

  it("adds a 2px label-value gap on read-only rows only", () => {
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    const trafficDt = screen.getByText("Трафик");
    expect(trafficDt.tagName).toBe("DT");
    expect(trafficDt.parentElement).toHaveClass("gap-0.5");

    const statusDt = screen.getByText("Статус");
    expect(statusDt.parentElement).toHaveClass("gap-0.5");

    const nameDt = screen.getByText("Имя");
    expect(nameDt.tagName).toBe("DT");
    expect(nameDt.parentElement).not.toHaveClass("gap-0.5");
    const nameStack = nameDt.closest("dl")?.querySelector("div");
    expect(nameStack).toBeTruthy();
    expect(nameStack).not.toHaveClass("gap-0.5");
  });

  it("uses 4px horizontal padding on inline name edit buttons", async () => {
    const user = userEvent.setup();

    render(
      <ClientInfoDialog
        client={client}
        onOpenChange={() => {}}
        onSave={vi.fn()}
      />,
    );

    const edit = screen.getByRole("button", { name: "Изменить имя" });
    expect(edit).toHaveClass("px-1");
    expect(edit).not.toHaveClass("px-2");

    await user.click(edit);

    const save = screen.getByRole("button", { name: "Сохранить имя" });
    const cancel = screen.getByRole("button", { name: "Отменить имя" });
    expect(save).toHaveClass("px-1");
    expect(save).not.toHaveClass("px-2");
    expect(cancel).toHaveClass("px-1");
    expect(cancel).not.toHaveClass("px-2");
  });
});

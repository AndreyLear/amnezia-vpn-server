import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ClientInfoDialog } from "@/components/ClientInfoDialog";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
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

  // The counters arrive from the server's point of view: rx is what it
  // received from the client (the client's upload), tx is what it sent
  // (the client's download). The arrows here describe the client, so ↓
  // must carry tx (amnezia-vpn-server-9l30).
  it("points the download arrow at the bytes the server sent", () => {
    render(
      <ClientInfoDialog
        client={{ ...client, rx_bytes: 1024 ** 3, tx_bytes: 5 * 1024 ** 3 }}
        onOpenChange={() => {}}
      />,
    );

    expect(screen.getByText("↓ 5,0 Гб · ↑ 1,0 Гб")).toBeInTheDocument();
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
    const enable = screen.getByRole("button", { name: "Включить" });
    expect(enable).toHaveTextContent("Включить");
    const icon = enable.querySelector("svg");
    expect(icon).not.toBeNull();
    expect(icon).toHaveAttribute("data-icon", "inline-start");
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

    expect(screen.getByRole("button", { name: "QR" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Конфиг" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Отключить" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Удалить" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "QR-код" })).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Скачать конфиг" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: `Действия для ${client.name}` }),
    ).not.toBeInTheDocument();
    expect(
      document.querySelector('[data-slot="dropdown-menu-trigger"]'),
    ).toBeNull();
  });

  it("puts Удалить inside the properties dl as the row after Трафик", () => {
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

    const remove = screen.getByRole("button", { name: "Удалить" });
    const dl = document.querySelector("dl");
    expect(dl).toHaveClass("divide-y", "divide-border");
    expect(dl).toContainElement(remove);

    const trafficRow = screen.getByText("Трафик").closest("div.flex");
    const deleteRow = remove.closest("div.flex");
    expect(trafficRow).not.toBeNull();
    expect(deleteRow).not.toBeNull();
    expect(trafficRow?.parentElement).toBe(dl);
    expect(deleteRow?.parentElement).toBe(dl);
    expect(
      trafficRow!.compareDocumentPosition(deleteRow!) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    expect(trafficRow!.nextElementSibling).toBe(deleteRow);

    expect(deleteRow).toHaveClass("pt-3");
    expect(trafficRow).toHaveClass("py-2");
    expect(trafficRow).not.toHaveClass("pt-3");

    expect(remove).not.toHaveClass("max-sm:h-12");
    expect(remove).not.toHaveClass("max-sm:w-full");
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
      "p-4",
      "pb-6",
    );

    const dl = document.querySelector("dl");
    expect(dl).toHaveClass("divide-y", "divide-border", "gap-0");
    expect(dl).not.toHaveClass("max-sm:divide-y", "max-sm:divide-border", "max-sm:gap-0");

    const trafficRow = screen.getByText("Трафик").closest("div.flex");
    expect(trafficRow).toHaveClass("flex", "items-center", "py-2");
    expect(trafficRow).not.toHaveClass("max-sm:py-2");
  });

  it("puts Отключить next to Статус in a centered property row", () => {
    render(
      <ClientInfoDialog
        client={client}
        onOpenChange={() => {}}
        onToggle={() => {}}
      />,
    );

    const toggle = screen.getByRole("button", { name: "Отключить" });
    const row = toggle.parentElement;
    expect(row).toHaveClass("flex", "items-center");
    expect(row).toContainElement(screen.getByText("Статус"));
    expect(row).toContainElement(screen.getByText("офлайн"));
    expect(toggle).toHaveTextContent("Отключить");
    const icon = toggle.querySelector("svg");
    expect(icon).not.toBeNull();
    expect(icon).toHaveAttribute("data-icon", "inline-start");
    expect(screen.getAllByRole("button", { name: "Отключить" })).toHaveLength(1);
  });

  it("puts Конфиг then QR on the IP row and Удалить in the dl, not a footer", () => {
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

    const ipRow = screen.getByText("IP").closest("div.flex");
    expect(ipRow).not.toBeNull();
    expect(ipRow).toHaveTextContent("10.8.0.2/32");
    const config = within(ipRow!).getByRole("button", { name: "Конфиг" });
    const qr = within(ipRow!).getByRole("button", { name: "QR" });
    expect(config).toHaveTextContent("Конфиг");
    expect(qr).toHaveTextContent("QR");
    expect(config.compareDocumentPosition(qr) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
    for (const button of [config, qr]) {
      expect(button).toHaveAttribute("data-variant", "outline");
      expect(button).toHaveAttribute("data-size", "default");
      const icon = button.querySelector("svg");
      expect(icon).not.toBeNull();
      expect(icon).toHaveAttribute("data-icon", "inline-start");
    }

    const remove = screen.getByRole("button", { name: "Удалить" });
    const dl = document.querySelector("dl");
    expect(dl).toContainElement(remove);
    expect(ipRow).not.toContainElement(remove);
    expect(remove.parentElement).not.toHaveClass("flex-wrap", "gap-2");
    expect(remove.closest("div.flex-wrap.gap-2")).toBeNull();
    expect(within(remove.closest("div.flex")!).getAllByRole("button")).toHaveLength(1);
    expect(within(dl!).queryByRole("button", { name: "QR" })).toBeInTheDocument();
    expect(remove).toHaveAttribute("data-variant", "destructive");
    expect(remove).toHaveAttribute("data-size", "default");
    expect(remove).not.toHaveClass("max-sm:h-12");
    expect(remove).not.toHaveClass("max-sm:w-full");

    const toggle = screen.getByRole("button", { name: "Отключить" });
    expect(remove.closest("div.flex")).not.toContainElement(toggle);
    expect(toggle.parentElement).toContainElement(screen.getByText("Статус"));
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
    for (const button of [editName, editDescription]) {
      const icon = button.querySelector("svg");
      expect(icon).not.toBeNull();
      expect(icon).toHaveAttribute("data-icon", "inline-start");
    }

    expect(document.querySelector("#info-name")).toBeNull();
    expect(document.querySelector("#info-description")).toBeNull();
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByRole("button", { name: "Сохранить" })).toBeNull();
  });

  it("opens name edit in a nested dialog and keeps Alice as read-only text", async () => {
    const user = userEvent.setup();

    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    await user.click(screen.getByRole("button", { name: "Изменить имя" }));

    const edit = screen.getByRole("dialog", { name: "Имя" });
    const clientDialog = screen
      .getByRole("heading", { name: "Клиент", hidden: true })
      .closest('[data-slot="dialog-content"]');
    expect(clientDialog).not.toBeNull();
    expect(edit.closest('[data-slot="dialog-content"]')).not.toHaveClass("pb-6");
    expect(within(edit).getByRole("textbox")).toHaveAttribute("id", "info-name");
    expect(within(clientDialog!).queryByRole("textbox")).toBeNull();
    const nameRow = within(clientDialog!).getByText("Имя").closest("div.flex");
    expect(nameRow?.querySelector("#info-name")).toBeNull();
    expect(nameRow?.querySelector("dd")).toHaveTextContent("Alice");
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

    const edit = screen.getByRole("dialog", { name: "Имя" });
    const name = within(edit).getByLabelText("Имя");
    expect(name).toHaveValue("Alice");
    await user.clear(name);
    await user.type(name, "Bob");
    await user.click(within(edit).getByRole("button", { name: "Close" }));

    expect(screen.queryByRole("dialog", { name: "Имя" })).not.toBeInTheDocument();
    expect(
      within(screen.getByRole("dialog", { name: "Клиент" })).getByText("Alice"),
    ).toBeInTheDocument();
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
    const name = within(screen.getByRole("dialog", { name: "Имя" })).getByLabelText(
      "Имя",
    );
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

    const description = within(
      screen.getByRole("dialog", { name: "Описание" }),
    ).getByLabelText(/Описание/);
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

  it("styles property-row Изменить and Отключить like the outline Backup trigger", () => {
    render(
      <ClientInfoDialog
        client={client}
        onOpenChange={() => {}}
        onToggle={() => {}}
      />,
    );

    for (const name of ["Изменить имя", "Изменить описание", "Отключить"] as const) {
      const button = screen.getByRole("button", { name });
      expect(button).toHaveAttribute("data-variant", "outline");
      expect(button).toHaveAttribute("data-size", "default");
      expect(button).toHaveClass("h-8");
      expect(button).not.toHaveClass("max-sm:h-12");
      expect(button).not.toHaveClass("px-1");
      expect(button).not.toHaveClass("ghost");
    }
  });

  it("gives Сохранить in the edit dialog a 48px mobile tap target", async () => {
    const user = userEvent.setup();

    render(
      <ClientInfoDialog
        client={client}
        onOpenChange={() => {}}
        onSave={vi.fn()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Изменить имя" }));

    const edit = screen.getByRole("dialog", { name: "Имя" });
    expect(within(edit).getByRole("button", { name: "Сохранить имя" })).toHaveClass(
      "max-sm:h-12",
    );
    expect(
      within(edit).queryByRole("button", { name: "Отменить имя" }),
    ).not.toBeInTheDocument();
  });

  it("puts default DialogContent at the bottom on small screens", () => {
    render(
      <Dialog open>
        <DialogContent>
          <DialogTitle>Тест</DialogTitle>
        </DialogContent>
      </Dialog>,
    );

    expect(document.querySelector('[data-slot="dialog-content"]')).toHaveClass(
      "max-sm:bottom-0",
    );
  });

  it("gives the Close button a 48px mobile tap target", () => {
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    const close = screen.getByRole("button", { name: "Close" });
    const content = document.querySelector('[data-slot="dialog-content"]');
    expect(content).toHaveClass("max-sm:[&_[data-size=icon-sm]]:size-12");
    expect(close).toHaveAttribute("data-size", "icon-sm");
    expect(
      document.querySelector('[data-slot="dialog-header"]'),
    ).toHaveClass("max-sm:pr-12");
  });

  it("gives delete confirm Отмена and Удалить 48px mobile tap targets", async () => {
    const user = userEvent.setup();

    render(
      <ClientInfoDialog
        client={client}
        onOpenChange={() => {}}
        onDelete={() => {}}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Удалить" }));

    const alert = screen.getByRole("alertdialog");
    expect(within(alert).getByRole("button", { name: "Отмена" })).toHaveClass(
      "max-sm:h-12",
    );
    expect(within(alert).getByRole("button", { name: "Удалить" })).toHaveClass(
      "max-sm:h-12",
    );
    expect(alert).toHaveClass("max-sm:max-w-none", "max-sm:w-full");
    expect(alert.className).not.toMatch(/data-\[size=default\]:max-w-xs(?!:)/);
  });
});

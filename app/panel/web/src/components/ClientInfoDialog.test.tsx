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
    expect(toggle.querySelector("svg")).toBeNull();
    expect(screen.getAllByRole("button", { name: "Отключить" })).toHaveLength(1);
  });

  it("keeps QR, download, and delete in the action row without enable/disable", () => {
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
    const actionRow = qr.parentElement;
    expect(actionRow).not.toBeNull();
    expect(within(actionRow!).getByRole("button", { name: "QR-код" })).toBeInTheDocument();
    expect(
      within(actionRow!).getByRole("button", { name: "Скачать конфиг" }),
    ).toBeInTheDocument();
    expect(within(actionRow!).getByRole("button", { name: "Удалить" })).toBeInTheDocument();
    expect(
      within(actionRow!).queryByRole("button", { name: "Отключить" }),
    ).not.toBeInTheDocument();

    const toggle = screen.getByRole("button", { name: "Отключить" });
    expect(actionRow).not.toContainElement(toggle);
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

  it("uses 4px horizontal padding on Изменить", () => {
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    const edit = screen.getByRole("button", { name: "Изменить имя" });
    expect(edit).toHaveClass("px-1");
    expect(edit).not.toHaveClass("px-2");
  });

  it("gives Изменить a 48px mobile tap target", () => {
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    expect(screen.getByRole("button", { name: "Изменить имя" })).toHaveClass(
      "max-sm:h-12",
    );
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

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
  it("edits name and description", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(undefined);

    render(
      <ClientInfoDialog
        client={client}
        onOpenChange={() => {}}
        onSave={onSave}
      />,
    );

    const name = screen.getByLabelText("Имя");
    const description = screen.getByLabelText("Описание");
    await user.clear(name);
    await user.type(name, "Bob");
    await user.clear(description);
    await user.type(description, "laptop");
    await user.click(screen.getByRole("button", { name: "Сохранить" }));

    expect(onSave).toHaveBeenCalledWith({ name: "Bob", description: "laptop" });
  });
});

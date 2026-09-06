import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ClientInfoDialog } from "@/components/ClientInfoDialog";
import type { Client } from "@/lib/api";

// Свой MTU у клиента (amnezia-vpn-server-h2pg). Серверное значение рассчитано
// на худшую последнюю милю среди всех; клиенту с заведомо лучшим каналом его
// можно поднять — и панель должна показывать, откуда взято действующее.
const client: Client = {
  id: 7,
  name: "router",
  description: "",
  address: "10.8.0.4/32",
  enabled: true,
  online: true,
  last_handshake_utc: null,
  rx_bytes: 0,
  tx_bytes: 0,
  mtu: 0,
};

describe("MTU клиента", () => {
  it("без своего значения говорит, что берёт серверное", () => {
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    expect(screen.getByText("MTU")).toBeInTheDocument();
    expect(screen.getByText("как у сервера")).toBeInTheDocument();
  });

  it("со своим значением показывает его", () => {
    render(
      <ClientInfoDialog client={{ ...client, mtu: 1420 }} onOpenChange={() => {}} />,
    );

    expect(screen.getByText("1420")).toBeInTheDocument();
    expect(screen.queryByText("как у сервера")).toBeNull();
  });

  it("сохраняет введённое значение", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(true);
    render(<ClientInfoDialog client={client} onSave={onSave} onOpenChange={() => {}} />);

    await user.click(screen.getByRole("button", { name: "Изменить MTU" }));
    const field = await screen.findByLabelText("Размер пакета, байт");
    await user.type(field, "1420");
    await user.click(screen.getByRole("button", { name: "Сохранить MTU" }));

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith(
        expect.objectContaining({ mtu: 1420 }),
      );
    });
  });

  // Пустое поле — способ вернуть клиента к серверному значению. Без этого
  // своё значение нельзя было бы снять, только заменить другим.
  it("пустое поле снимает своё значение", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(true);
    render(
      <ClientInfoDialog
        client={{ ...client, mtu: 1420 }}
        onSave={onSave}
        onOpenChange={() => {}}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Изменить MTU" }));
    const field = await screen.findByLabelText("Размер пакета, байт");
    await user.clear(field);
    await user.click(screen.getByRole("button", { name: "Сохранить MTU" }));

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ mtu: 0 }));
    });
  });
});

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ClientInfoDialog } from "@/components/ClientInfoDialog";
import type { Client } from "@/lib/api";

// Выданный конфиг несёт оба адреса — панель должна показывать оба, иначе
// владелец не видит того, что роздал (amnezia-vpn-server-lhlv).
const client: Client = {
  id: 3,
  name: "router",
  description: "",
  address: "10.8.0.4/32",
  address6: "",
  enabled: true,
  online: true,
  last_handshake_utc: null,
  rx_bytes: 0,
  tx_bytes: 0,
  mtu: 0,
  dns_bypass: false,
};

describe("адрес клиента в карточке", () => {
  it("на туннеле без IPv6 показывает один адрес", () => {
    render(<ClientInfoDialog client={client} onOpenChange={() => {}} />);

    expect(screen.getByText("10.8.0.4/32")).toBeInTheDocument();
    expect(screen.queryByText(/fd/)).toBeNull();
  });

  it("на туннеле с IPv6 показывает оба", () => {
    render(
      <ClientInfoDialog
        client={{ ...client, address6: "fded:a0b:d921::4/128" }}
        onOpenChange={() => {}}
      />,
    );

    expect(screen.getByText("10.8.0.4/32")).toBeInTheDocument();
    expect(screen.getByText("fded:a0b:d921::4/128")).toBeInTheDocument();
  });
});

import { describe, expect, it } from "vitest";

import type { Client } from "@/lib/api";
import { lastProtocol, protocolActivity } from "@/lib/protocol";

const base: Client = {
  id: 1,
  name: "a",
  description: "",
  address: "10.8.0.2/32",
  address6: "",
  enabled: true,
  online: false,
  last_handshake_utc: null,
  rx_bytes: 10,
  tx_bytes: 20,
  mtu: 0,
  rate_limit: 0,
  dns_bypass: false,
};

// amnezia-vpn-server-14y0.8: строка и окно клиента показывают протокол, по
// которому клиент был на связи последним.
describe("lastProtocol", () => {
  it("без активности — AWG", () => {
    expect(lastProtocol(base)).toBe("awg");
  });

  it("только VLESS — VLESS", () => {
    expect(lastProtocol({ ...base, vless_last_active_utc: "2026-10-27T10:00:00Z" })).toBe("vless");
  });

  it("свежее из двух", () => {
    const both = {
      ...base,
      last_handshake_utc: "2026-10-27T10:00:00Z",
      vless_last_active_utc: "2026-10-27T11:00:00Z",
    };
    expect(lastProtocol(both)).toBe("vless");
    expect(lastProtocol({ ...both, last_handshake_utc: "2026-10-27T12:00:00Z" })).toBe("awg");
  });

  it("сервер без полей VLESS — AWG", () => {
    expect(lastProtocol({ ...base, last_handshake_utc: "2026-10-27T10:00:00Z" })).toBe("awg");
  });
});

describe("protocolActivity", () => {
  it("AWG переворачивает rx/tx сервера в направления клиента", () => {
    expect(protocolActivity(base, "awg")).toEqual({ lastSeen: null, online: false, down: 20, up: 10 });
  });

  it("VLESS берёт свои счётчики как есть", () => {
    const c = {
      ...base,
      vless_up_bytes: 1,
      vless_down_bytes: 2,
      vless_online: true,
      vless_last_active_utc: "2026-10-27T10:00:00Z",
    };
    expect(protocolActivity(c, "vless")).toEqual({
      lastSeen: "2026-10-27T10:00:00Z",
      online: true,
      down: 2,
      up: 1,
    });
  });
});

import type { Client } from "@/lib/api";

// Протокол, через который клиент был на связи последним
// (amnezia-vpn-server-14y0.8). Владелец: «показывать последний трафик,
// откуда был — либо с Xray, либо с AWG; не вижу смысла показывать оба,
// некоторые клиенты пользуются только Xray». По нему строка на главном
// экране выбирает, что показать, а окно клиента — какую вкладку открыть.

export type Protocol = "awg" | "vless";

export const protocolLabel: Record<Protocol, string> = {
  awg: "AWG",
  vless: "VLESS",
};

function time(iso: string | null | undefined): number | null {
  if (!iso) return null;
  const t = new Date(iso).getTime();
  return Number.isNaN(t) ? null : t;
}

/**
 * Свежее из двух: рукопожатие AWG или движение по VLESS. Кто ни разу не
 * был на связи — AWG, как было до VLESS.
 */
export function lastProtocol(client: Client): Protocol {
  const awg = time(client.last_handshake_utc);
  const vless = time(client.vless_last_active_utc);
  if (vless !== null && (awg === null || vless > awg)) return "vless";
  return "awg";
}

/** Последняя активность, трафик и «на связи» по протоколу. */
export function protocolActivity(client: Client, protocol: Protocol) {
  if (protocol === "vless") {
    return {
      lastSeen: client.vless_last_active_utc ?? null,
      online: client.vless_online ?? false,
      down: client.vless_down_bytes ?? 0,
      up: client.vless_up_bytes ?? 0,
    };
  }
  // rx_bytes и tx_bytes AWG — с точки зрения сервера: rx — принято
  // сервером, то есть отдача клиента (amnezia-vpn-server-9l30).
  return {
    lastSeen: client.last_handshake_utc,
    online: client.online,
    down: client.tx_bytes,
    up: client.rx_bytes,
  };
}

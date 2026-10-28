import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DailyTrafficChart } from "@/components/DailyTrafficChart";
import type { TrafficHour } from "@/lib/dailyTraffic";

const fetchTraffic = vi.hoisted(() => vi.fn());
vi.mock("@/lib/api", async (orig) => ({
  ...(await orig<typeof import("@/lib/api")>()),
  fetchTraffic,
}));

const GiB = 1024 ** 3;
const MiB = 1024 ** 2;
const savedTZ = process.env.TZ;

function hour(utc: string, h: Partial<TrafficHour> = {}): TrafficHour {
  return { hour_utc: utc, awg_down: 0, awg_up: 0, vless_down: 0, vless_up: 0, ...h };
}

function bars(series: string): SVGRectElement[] {
  return Array.from(document.querySelectorAll(`svg rect[data-series="${series}"]`));
}

function hover(day: number) {
  const svg = document.querySelector("svg");
  const plot = svg?.parentElement;
  if (!svg || !plot) throw new Error("нет графика");
  plot.getBoundingClientRect = () => ({ left: 0, width: 300, top: 0, height: 120 }) as DOMRect;
  fireEvent.pointerMove(svg, { clientX: day * 10 + 5, clientY: 60 });
}

beforeEach(() => {
  fetchTraffic.mockReset();
  process.env.TZ = "Asia/Novosibirsk";
  vi.useFakeTimers({ toFake: ["Date"] });
  // 28 окт 10:00 местного (UTC+7).
  vi.setSystemTime(new Date("2026-10-28T03:00:00Z"));
});

afterEach(() => {
  vi.useRealTimers();
  process.env.TZ = savedTZ;
});

describe("DailyTrafficChart", () => {
  it("запрашивает часы с местной полуночи первого из тридцати дней", async () => {
    fetchTraffic.mockResolvedValue({ from_utc: "", to_utc: "", hours: [] });
    render(<DailyTrafficChart clientId={7} protocol="awg" />);
    await waitFor(() => expect(fetchTraffic).toHaveBeenCalled());
    const [id, from] = fetchTraffic.mock.calls[0];
    expect(id).toBe(7);
    expect((from as Date).toISOString()).toBe("2026-09-28T17:00:00.000Z");
  });

  // График стоит во вкладке протокола, как график скорости, и показывает
  // только этот протокол (amnezia-vpn-server-gzmb, доработка владельца).
  it("на вкладке AWG — только AWG: скачал и отдал по дням", async () => {
    fetchTraffic.mockResolvedValue({
      from_utc: "",
      to_utc: "",
      hours: [
        hour("2026-10-28T01:00:00Z", { awg_down: 2 * GiB, awg_up: 100 * MiB, vless_down: 9 * GiB }),
        hour("2026-10-20T01:00:00Z", { awg_down: GiB }),
        hour("2026-10-15T01:00:00Z", { vless_down: GiB, vless_up: GiB }),
      ],
    });
    render(<DailyTrafficChart clientId={7} protocol="awg" />);
    await waitFor(() => expect(bars("down")).toHaveLength(2));
    expect(bars("up")).toHaveLength(1);
    // Единица — в заголовке, ось без единиц: шкала по AWG, до 2 Гб; VLESS
    // в 9 Гб её не раздувает.
    expect(screen.getByText("Трафик по дням, Гб")).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
    // Легенды протоколов нет: на графике один протокол.
    expect(screen.queryByText("AWG")).toBeNull();
    expect(screen.queryByText("VLESS")).toBeNull();
    expect(screen.getByText("Скачал")).toBeInTheDocument();
    expect(screen.getByText("Отдал")).toBeInTheDocument();
    expect(screen.getByText("Сегодня")).toBeInTheDocument();
    expect(screen.getByText("29 сен")).toBeInTheDocument();
    expect(screen.getByRole("img")).toHaveAccessibleName(
      "Трафик по дням за 30 дней: больше всего 2,1 Гб, 28 окт",
    );
  });

  it("на вкладке VLESS — только VLESS", async () => {
    fetchTraffic.mockResolvedValue({
      from_utc: "",
      to_utc: "",
      hours: [
        hour("2026-10-28T01:00:00Z", { awg_down: 2 * GiB, vless_down: GiB }),
        hour("2026-10-20T01:00:00Z", { awg_down: GiB }),
      ],
    });
    render(<DailyTrafficChart clientId={7} protocol="vless" />);
    await waitFor(() => expect(bars("down")).toHaveLength(1));
    expect(bars("down")[0].getAttribute("data-day")).toBe("29");
    expect(bars("up")).toHaveLength(0);
  });

  it("подсказка дня — цифры одного протокола, без разбивки", async () => {
    fetchTraffic.mockResolvedValue({
      from_utc: "",
      to_utc: "",
      hours: [
        // 27 окт 17:00 UTC — уже 28 окт по местному.
        hour("2026-10-27T17:00:00Z", { awg_down: GiB, awg_up: 60 * MiB, vless_down: 2 * GiB }),
        hour("2026-10-20T01:00:00Z", { awg_down: GiB }),
      ],
    });
    render(<DailyTrafficChart clientId={7} protocol="awg" />);
    await waitFor(() => expect(bars("down")).toHaveLength(2));

    hover(29);
    const tip = screen.getByRole("status");
    expect(tip).toHaveTextContent(/^28 окт↓ 1,0 Гб · ↑ 0,1 Гб$/);

    hover(5);
    expect(screen.getByRole("status")).toHaveTextContent("Трафика не было");
  });

  // Трафик был только по другому протоколу — для этой вкладки его нет.
  it("трафик только другого протокола — заглушка", async () => {
    fetchTraffic.mockResolvedValue({
      from_utc: "",
      to_utc: "",
      hours: [hour("2026-10-28T01:00:00Z", { vless_down: GiB })],
    });
    render(<DailyTrafficChart clientId={7} protocol="awg" />);
    expect(await screen.findByText("За 30 дней трафика нет")).toBeInTheDocument();
  });

  it("без трафика — заглушка с объяснением, откуда история", async () => {
    fetchTraffic.mockResolvedValue({ from_utc: "", to_utc: "", hours: [] });
    render(<DailyTrafficChart clientId={7} protocol="awg" />);
    expect(await screen.findByText("За 30 дней трафика нет")).toBeInTheDocument();
    expect(screen.getByText("История копится с обновления панели")).toBeInTheDocument();
    expect(screen.getByText("Трафик по дням")).toBeInTheDocument();
  });

  it("ошибка чтения — своя заглушка", async () => {
    fetchTraffic.mockRejectedValue(new Error("boom"));
    render(<DailyTrafficChart clientId={7} protocol="awg" />);
    expect(await screen.findByText("Историю прочитать не удалось")).toBeInTheDocument();
  });
});

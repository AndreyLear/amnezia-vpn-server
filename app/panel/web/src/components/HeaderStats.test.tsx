import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { HeaderStats } from "@/components/HeaderStats";
import type { HostSnapshot } from "@/lib/api";
import { formatBytes } from "@/lib/format";

const DASH = "\u2014";
const LABEL_RE = /cpu|ram|disk|CPU|RAM|Диск/;

const emptyHost: HostSnapshot = {
  cpu_percent: null,
  ram_percent: null,
  disk_percent: null,
  ram_used_bytes: null,
  ram_total_bytes: null,
  disk_used_bytes: null,
  disk_total_bytes: null,
  iface: "awg0",
  iface_state: "na",
};

const diskUsed = 4 * 1024 ** 3;
const diskTotal = 25 * 1024 ** 3;
const ramUsed = Math.round(1.9 * 1024 ** 3);
const ramTotal = Math.round(7.6 * 1024 ** 3);
const diskUsedTb = Math.round(0.3 * 1024 ** 4);

function expectValuesOnly(el: HTMLElement) {
  expect(el.textContent ?? "").not.toMatch(LABEL_RE);
}

describe("HeaderStats", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("shows values only without cpu / ram / disk labels", () => {
    render(
      <HeaderStats
        host={{
          ...emptyHost,
          cpu_percent: 100,
          ram_percent: 75,
          disk_used_bytes: diskUsedTb,
        }}
      />,
    );

    expect(screen.getByLabelText("CPU")).toHaveTextContent("100%");
    expect(screen.getByLabelText("RAM")).toHaveTextContent("75%");
    expect(screen.getByLabelText("Диск")).toHaveTextContent("0,3 Тб");
    expectValuesOnly(screen.getByLabelText("CPU"));
    expectValuesOnly(screen.getByLabelText("RAM"));
    expectValuesOnly(screen.getByLabelText("Диск"));
    expect(screen.queryByText(/^cpu$/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/^ram$/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/^disk$/i)).not.toBeInTheDocument();
    expect(screen.queryByText("Диск")).not.toBeInTheDocument();

    const slashes = screen.getAllByText("/");
    expect(slashes).toHaveLength(2);
    for (const slash of slashes) {
      expect(slash).toHaveClass("text-muted-foreground");
      expect(slash).toHaveAttribute("aria-hidden", "true");
      expect(slash.closest("button")).toBeNull();
      expect(slash.closest("[role='button']")).toBeNull();
    }
  });

  it("uses a 4px gap on the stats row instead of 12px", () => {
    render(
      <HeaderStats
        host={{
          ...emptyHost,
          cpu_percent: 100,
          ram_percent: 75,
          disk_used_bytes: diskUsedTb,
        }}
      />,
    );

    const loadRow = screen.getByLabelText("CPU").closest("[class*='gap-1']");
    expect(loadRow).toHaveClass("gap-1");
    expect(loadRow).not.toHaveClass("gap-3");
  });

  it("does not roll-animate the slash separators when values change", () => {
    const { rerender } = render(
      <HeaderStats host={{ ...emptyHost, cpu_percent: 50, ram_percent: 40 }} />,
    );
    rerender(
      <HeaderStats host={{ ...emptyHost, cpu_percent: 75, ram_percent: 40 }} />,
    );

    const slashes = screen.getAllByText("/");
    expect(slashes).toHaveLength(2);
    for (const slash of slashes) {
      expect(slash).not.toHaveClass("animate-in");
      expect(slash).not.toHaveClass("animate-out");
      expect(slash.querySelector(".animate-in")).toBeNull();
      expect(slash.querySelector(".animate-out")).toBeNull();
    }
  });

  it("shows em dashes for null cpu, ram, and disk", () => {
    render(<HeaderStats host={emptyHost} />);

    expect(screen.getByLabelText("CPU")).toHaveTextContent(DASH);
    expect(screen.getByLabelText("RAM")).toHaveTextContent(DASH);
    expect(screen.getByLabelText("Диск")).toHaveTextContent(DASH);
    expectValuesOnly(screen.getByLabelText("CPU"));
    expect(screen.queryByText(/cpu\s+-/)).not.toBeInTheDocument();
  });

  it("shows em dashes when host is null", () => {
    render(<HeaderStats host={null} />);

    expect(screen.getByLabelText("CPU")).toHaveTextContent(DASH);
    expect(screen.getByLabelText("RAM")).toHaveTextContent(DASH);
    expect(screen.getByLabelText("Диск")).toHaveTextContent(DASH);
  });

  it("shows cpu as a rounded integer percent with a percent sign", () => {
    render(
      <HeaderStats
        host={{
          ...emptyHost,
          cpu_percent: 12.6,
          ram_percent: 40.4,
          disk_percent: 9.5,
          disk_used_bytes: diskUsed,
        }}
      />,
    );

    expect(screen.getByLabelText("CPU")).toHaveTextContent("13%");
    expectValuesOnly(screen.getByLabelText("CPU"));
  });

  it("shows ram as an integer percent with a percent sign", () => {
    render(
      <HeaderStats
        host={{
          ...emptyHost,
          ram_percent: 75.4,
        }}
      />,
    );

    expect(screen.getByLabelText("RAM")).toHaveTextContent("75%");
    expectValuesOnly(screen.getByLabelText("RAM"));
  });

  it("shows disk occupied space via formatBytes of used bytes", () => {
    render(
      <HeaderStats
        host={{
          ...emptyHost,
          disk_percent: 16,
          disk_used_bytes: diskUsed,
          disk_total_bytes: diskTotal,
        }}
      />,
    );

    expect(screen.getByLabelText("Диск")).toHaveTextContent(formatBytes(diskUsed));
    expectValuesOnly(screen.getByLabelText("Диск"));
  });

  it("shows CPU tooltip with load copy and percent, without a period", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(
      <HeaderStats
        host={{
          ...emptyHost,
          cpu_percent: 12.6,
        }}
      />,
    );

    await user.hover(screen.getByLabelText("CPU"));
    const tip = await screen.findByRole("tooltip");
    expect(tip).toHaveTextContent("Загрузка CPU: 13%");
    expect(tip.textContent).not.toMatch(/\.$/);
  });

  it("shows RAM tooltip with percent and used/total bytes", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(
      <HeaderStats
        host={{
          ...emptyHost,
          ram_percent: 75,
          ram_used_bytes: ramUsed,
          ram_total_bytes: ramTotal,
        }}
      />,
    );

    await user.hover(screen.getByLabelText("RAM"));
    const tip = await screen.findByRole("tooltip");
    expect(tip).toHaveTextContent(
      `Загрузка RAM: 75% (${formatBytes(ramUsed)} / ${formatBytes(ramTotal)})`,
    );
    expect(tip.textContent).not.toMatch(/\.$/);
  });

  it("shows disk tooltip with percent and used/total bytes", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(
      <HeaderStats
        host={{
          ...emptyHost,
          disk_percent: 16,
          disk_used_bytes: diskUsed,
          disk_total_bytes: diskTotal,
        }}
      />,
    );

    await user.hover(screen.getByLabelText("Диск"));
    const tip = await screen.findByRole("tooltip");
    expect(tip).toHaveTextContent(
      `Загрузка диска: 16% (${formatBytes(diskUsed)} / ${formatBytes(diskTotal)})`,
    );
    expect(tip.textContent).not.toMatch(/\.$/);
  });

  it("does not show a tooltip when the metric is null", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<HeaderStats host={emptyHost} />);

    await user.hover(screen.getByLabelText("CPU"));
    await new Promise((resolve) => setTimeout(resolve, 300));
    expect(screen.queryByRole("tooltip")).toBeNull();
  });

  it("colors values muted below 70, amber below 90, and red otherwise", () => {
    render(
      <HeaderStats
        host={{
          ...emptyHost,
          cpu_percent: 50,
          ram_percent: 75,
          disk_percent: 95,
          ram_used_bytes: ramUsed,
          ram_total_bytes: ramTotal,
          disk_used_bytes: diskUsed,
          disk_total_bytes: diskTotal,
        }}
      />,
    );

    expect(screen.getByText("50%")).toHaveClass("text-muted-foreground");
    expect(screen.getByText("50%")).not.toHaveClass("text-emerald-500");
    expect(screen.getByText("75%")).toHaveClass("text-amber-500");
    expect(screen.getByText(formatBytes(diskUsed))).toHaveClass("text-red-500");
  });

  it("keeps dashes muted", () => {
    render(<HeaderStats host={emptyHost} />);

    const dashes = screen.getAllByText(DASH);
    expect(dashes).toHaveLength(3);
    for (const dash of dashes) {
      expect(dash).toHaveClass("text-muted-foreground");
    }
  });

  it("is not a button", () => {
    render(
      <HeaderStats
        host={{
          ...emptyHost,
          cpu_percent: 1,
          ram_percent: 2,
          disk_percent: 3,
          ram_used_bytes: ramUsed,
          ram_total_bytes: ramTotal,
          disk_used_bytes: diskUsed,
          disk_total_bytes: diskTotal,
        }}
      />,
    );

    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    const cpu = screen.getByLabelText("CPU");
    expect(cpu.closest("button")).toBeNull();
    expect(cpu.closest("[role='button']")).toBeNull();
    expect(cpu).not.toHaveClass("cursor-pointer");
    expect(cpu.parentElement).not.toHaveClass("cursor-pointer");
  });

  it("rolls the CPU value from the top and clips overflow while both values are on screen", () => {
    const { rerender } = render(
      <HeaderStats host={{ ...emptyHost, cpu_percent: 50 }} />,
    );

    expect(screen.getByLabelText("CPU")).toHaveTextContent("50%");
    expect(screen.queryByText("75%")).not.toBeInTheDocument();

    rerender(<HeaderStats host={{ ...emptyHost, cpu_percent: 75 }} />);

    expect(screen.getByText("50%")).toBeInTheDocument();
    expect(screen.getByText("75%")).toBeInTheDocument();
    expect(
      screen.getByText("75%").closest(".overflow-hidden"),
    ).not.toBeNull();

    fireEvent.animationEnd(screen.getByText("50%"));

    expect(screen.queryByText("50%")).not.toBeInTheDocument();
    expect(screen.getByText("75%")).toBeInTheDocument();
    expect(screen.getByLabelText("CPU")).toHaveTextContent("75%");
  });

  it("remounts the incoming CPU value so a second roll can replay", () => {
    const { rerender } = render(
      <HeaderStats host={{ ...emptyHost, cpu_percent: 50 }} />,
    );

    rerender(<HeaderStats host={{ ...emptyHost, cpu_percent: 75 }} />);

    const outgoing50 = screen.getByText("50%");
    const incoming75 = screen.getByText("75%");
    expect(outgoing50).toHaveClass("animate-out", "slide-out-to-bottom");
    expect(incoming75).toHaveClass("animate-in", "slide-in-from-top");
    expect(incoming75.closest(".overflow-hidden")).not.toBeNull();

    fireEvent.animationEnd(outgoing50);

    const settled75 = screen.getByText("75%");

    rerender(<HeaderStats host={{ ...emptyHost, cpu_percent: 80 }} />);

    const incoming80 = screen.getByText("80%");
    expect(incoming80).not.toBe(settled75);
    expect(incoming80).toHaveClass("animate-in", "slide-in-from-top");
    expect(screen.getByText("75%")).toHaveClass(
      "animate-out",
      "slide-out-to-bottom",
    );
    expect(incoming80.closest(".overflow-hidden")).not.toBeNull();
  });

  it("applies enter classes on the first commit of a remounted CPU value", () => {
    const { rerender } = render(
      <HeaderStats host={{ ...emptyHost, cpu_percent: 50 }} />,
    );

    const firstCommitClasses: string[] = [];
    const snapshotIfIncoming = (node: Node) => {
      if (
        node instanceof HTMLElement &&
        node.textContent === "75%" &&
        node.childElementCount === 0
      ) {
        firstCommitClasses.push(node.getAttribute("class") ?? "");
      }
    };
    const appendChild = Node.prototype.appendChild;
    const insertBefore = Node.prototype.insertBefore;
    Node.prototype.appendChild = function (this: Node, node: Node) {
      snapshotIfIncoming(node);
      return appendChild.call(this, node);
    };
    Node.prototype.insertBefore = function (
      this: Node,
      node: Node,
      child: Node | null,
    ) {
      snapshotIfIncoming(node);
      return insertBefore.call(this, node, child);
    };

    try {
      rerender(<HeaderStats host={{ ...emptyHost, cpu_percent: 75 }} />);
    } finally {
      Node.prototype.appendChild = appendChild;
      Node.prototype.insertBefore = insertBefore;
    }

    expect(firstCommitClasses.length).toBeGreaterThan(0);
    expect(
      firstCommitClasses.some((className) => className.includes("animate-in")),
    ).toBe(true);
    expect(
      firstCommitClasses.some((className) =>
        className.includes("slide-in-from-top"),
      ),
    ).toBe(true);
  });

  it("swaps the CPU value instantly when the user prefers reduced motion", () => {
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: query === "(prefers-reduced-motion: reduce)",
      media: query,
      onchange: null,
      addListener() {},
      removeListener() {},
      addEventListener() {},
      removeEventListener() {},
      dispatchEvent() {
        return false;
      },
    }));

    const { rerender } = render(
      <HeaderStats host={{ ...emptyHost, cpu_percent: 50 }} />,
    );
    rerender(<HeaderStats host={{ ...emptyHost, cpu_percent: 75 }} />);

    expect(screen.getByText("75%")).toBeInTheDocument();
    expect(screen.queryByText("50%")).not.toBeInTheDocument();
    expect(screen.getByLabelText("CPU")).toHaveTextContent("75%");
  });

  it("stacks the interface name above the load row in a 12px two-line column", () => {
    render(
      <HeaderStats
        host={{
          ...emptyHost,
          cpu_percent: 10,
          iface: "awg0",
          iface_state: "up",
        } as HostSnapshot}
      />,
    );

    const iface = screen.getByLabelText("Интерфейс");
    expect(iface).toHaveTextContent("awg0");
    expect(iface).not.toHaveTextContent(DASH);
    expect(iface).toHaveClass("text-xs", "w-fit");
    expect(iface).not.toHaveClass("text-sm");

    const loadRow = screen.getByLabelText("CPU").closest("[class*='tabular-nums']");
    expect(loadRow).toHaveClass("gap-1");
    expect(loadRow).not.toHaveClass("gap-3");
    expect(loadRow).not.toHaveClass("text-sm");
    expect(loadRow).not.toContainElement(iface);

    const cluster = iface.closest("[class*='flex-col']");
    expect(cluster).toHaveClass("flex", "flex-col", "gap-1", "text-xs");
    expect(cluster).not.toHaveClass("gap-3");
    expect(cluster).not.toHaveClass("text-sm");
    expect(cluster).toContainElement(loadRow as HTMLElement);

    expect(
      iface.compareDocumentPosition(loadRow as HTMLElement) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("colors the interface name by iface_state", () => {
    const { rerender } = render(
      <HeaderStats
        host={{ ...emptyHost, iface: "awg0", iface_state: "up" } as HostSnapshot}
      />,
    );
    expect(screen.getByLabelText("Интерфейс")).toHaveClass("text-muted-foreground");
    expect(screen.getByLabelText("Интерфейс")).not.toHaveClass("header-iface-shimmer");

    rerender(
      <HeaderStats
        host={{ ...emptyHost, iface: "awg0", iface_state: "error" } as HostSnapshot}
      />,
    );
    expect(screen.getByLabelText("Интерфейс")).toHaveClass("text-amber-500");

    rerender(
      <HeaderStats
        host={{ ...emptyHost, iface: "awg0", iface_state: "down" } as HostSnapshot}
      />,
    );
    expect(screen.getByLabelText("Интерфейс")).toHaveClass("text-red-500");
  });

  it("applies a glyph shimmer when host is null or iface_state is na", () => {
    const { rerender } = render(<HeaderStats host={null} />);
    const loading = screen.getByLabelText("Интерфейс");
    expect(loading).toHaveClass("header-iface-shimmer");
    expect(loading.textContent).toMatch(/awg0/);
    expect(loading).not.toHaveTextContent(DASH);

    rerender(
      <HeaderStats
        host={{ ...emptyHost, iface: "awg0", iface_state: "na" } as HostSnapshot}
      />,
    );
    expect(screen.getByLabelText("Интерфейс")).toHaveClass("header-iface-shimmer");
  });

  it("keeps muted static iface text when reduced motion is preferred", () => {
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: query === "(prefers-reduced-motion: reduce)",
      media: query,
      onchange: null,
      addListener() {},
      removeListener() {},
      addEventListener() {},
      removeEventListener() {},
      dispatchEvent() {
        return false;
      },
    }));

    render(
      <HeaderStats
        host={{ ...emptyHost, iface: "awg0", iface_state: "na" } as HostSnapshot}
      />,
    );
    const iface = screen.getByLabelText("Интерфейс");
    expect(iface).toHaveClass("text-muted-foreground");
    expect(iface).not.toHaveClass("header-iface-shimmer");
  });

  it.each([
    ["up", "Интерфейс поднят"],
    ["na", "Нет данных статуса"],
    ["down", "Интерфейс недоступен"],
    ["error", "Ошибка чтения статуса"],
  ] as const)("shows Russian tooltip for iface_state %s without a period", async (state, copy) => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(
      <HeaderStats
        host={{ ...emptyHost, iface: "awg0", iface_state: state } as HostSnapshot}
      />,
    );
    await user.hover(screen.getByLabelText("Интерфейс"));
    const tip = await screen.findByRole("tooltip");
    expect(tip).toHaveTextContent(copy);
    expect(tip.textContent).not.toMatch(/\.$/);
    expect(tip).toHaveAttribute("data-side", "right");
  });
});

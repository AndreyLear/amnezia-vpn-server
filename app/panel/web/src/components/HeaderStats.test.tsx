import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

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
    expect(tip).toHaveTextContent("Загрузка CPU 13%");
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
      `Загрузка RAM 75% (${formatBytes(ramUsed)} / ${formatBytes(ramTotal)})`,
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
      `Загрузка диска 16% (${formatBytes(diskUsed)} / ${formatBytes(diskTotal)})`,
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

  it("colors values emerald below 70, amber below 90, and red otherwise", () => {
    render(
      <HeaderStats
        host={{
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

    expect(screen.getByText("50%")).toHaveClass("text-emerald-500");
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
});

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { HeaderStats } from "@/components/HeaderStats";

const DASH = "\u2014";

describe("HeaderStats", () => {
  it("shows em dashes for null CPU, RAM, and Диск", () => {
    render(
      <HeaderStats
        host={{ cpu_percent: null, ram_percent: null, disk_percent: null }}
      />,
    );

    expect(screen.getByText(/^CPU/)).toHaveTextContent(`CPU ${DASH}`);
    expect(screen.getByText(/^RAM/)).toHaveTextContent(`RAM ${DASH}`);
    expect(screen.getByText(/^Диск/)).toHaveTextContent(`Диск ${DASH}`);
    expect(screen.queryByText(/CPU\s+-/)).not.toBeInTheDocument();
  });

  it("shows em dashes when host is null", () => {
    render(<HeaderStats host={null} />);

    expect(screen.getByText(/^CPU/)).toHaveTextContent(`CPU ${DASH}`);
    expect(screen.getByText(/^RAM/)).toHaveTextContent(`RAM ${DASH}`);
    expect(screen.getByText(/^Диск/)).toHaveTextContent(`Диск ${DASH}`);
  });

  it("rounds percents to integers with Math.round", () => {
    render(
      <HeaderStats
        host={{ cpu_percent: 12.6, ram_percent: 40.4, disk_percent: 9.5 }}
      />,
    );

    expect(screen.getByText(/^CPU/)).toHaveTextContent("CPU 13");
    expect(screen.getByText(/^RAM/)).toHaveTextContent("RAM 40");
    expect(screen.getByText(/^Диск/)).toHaveTextContent("Диск 10");
  });

  it("is not a button", () => {
    render(
      <HeaderStats
        host={{ cpu_percent: 1, ram_percent: 2, disk_percent: 3 }}
      />,
    );

    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    const cpu = screen.getByText(/^CPU/);
    expect(cpu.closest("button")).toBeNull();
    expect(cpu.closest("[role='button']")).toBeNull();
    expect(cpu.parentElement).not.toHaveClass("cursor-pointer");
  });
});

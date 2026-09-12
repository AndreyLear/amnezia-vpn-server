import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useCreepingProgress } from "@/lib/updateProgress";

afterEach(() => {
  vi.useRealTimers();
});

// Полоса прогресса не привязана ко времени обновления на сервере: панель
// перезапускается посреди установки, и живой ход отдавать некому. Хук идёт
// к 99% и там замирает, дожидаясь настоящего итога из файла состояния
// (amnezia-vpn-server-mrjh). Этот же хук теперь общий для окна и для тоста
// (amnezia-vpn-server-ekvi), поэтому его поведение проверяется отдельно, а
// не только через рендер UpdateDialog.
describe("useCreepingProgress", () => {
  it("никогда не доходит до ста, пока итога нет", async () => {
    vi.useFakeTimers();
    const { result } = renderHook(({ running }) => useCreepingProgress(running), {
      initialProps: { running: true },
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(60 * 60 * 1000);
    });

    expect(result.current).toBeLessThan(100);
    // И не стоит на месте: за час она обязана уйти далеко от нуля.
    expect(result.current).toBeGreaterThan(50);
  });

  it("стоит на нуле, пока обновление не идёт", () => {
    const { result } = renderHook(({ running }) => useCreepingProgress(running), {
      initialProps: { running: false },
    });
    expect(result.current).toBe(0);
  });
});

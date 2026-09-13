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

// Владелец: «сперва прогресс начинается с трети, потом уходит в 0 и потом
// снова работает нормально». Процент оставался от прошлого прогона, и новый
// прогон первым кадром рисовал его (amnezia-vpn-server-evv3).
describe("новый прогон начинается с нуля", () => {
  it("после окончания обновления процент возвращается к нулю", async () => {
    vi.useFakeTimers();
    const { result, rerender } = renderHook(({ running }) => useCreepingProgress(running), {
      initialProps: { running: true },
    });
    // Около ста секунд — та самая «треть».
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100_000);
    });
    expect(result.current).toBeGreaterThan(25);

    rerender({ running: false });
    expect(result.current).toBe(0);

    // И следующий прогон стартует с нуля с первого же кадра.
    rerender({ running: true });
    expect(result.current).toBe(0);
  });
});

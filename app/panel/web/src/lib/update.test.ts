import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { UpdateInfo } from "@/lib/api";
import { PANEL_RESTART_TIMEOUT_MS, useUpdateInfo } from "@/lib/update";

// useUpdateInfo переживает перезапуск панели (amnezia-vpn-server-mrjh).
//
// Шаг установки перезапускает саму панель: опрос "/api/update" в эту
// секунду либо падает по сети, либо прокси перед ещё не поднявшейся
// панелью отвечает вместо неё error-страницей. Ни то, ни другое не должно
// читаться как «обновление не удалось» — это ожидаемая часть перезапуска.

function updateInfo(overrides: Partial<UpdateInfo> = {}): UpdateInfo {
  return {
    installed: "2.9.0",
    latest: "2.10.0",
    available: true,
    notes: "",
    checked_at_utc: "",
    check_result: "",
    check_reason: "",
    state: "running",
    state_from: "2.9.0",
    state_to: "2.10.0",
    state_step: "установка",
    state_message: "",
    state_at_utc: "",
    dismissed: "",
    outcome_seen: "",
    ...overrides,
  };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("useUpdateInfo", () => {
  it("упавший по сети запрос не стирает «идёт обновление» и опрос продолжается", async () => {
    let calls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        calls += 1;
        if (calls === 1) return jsonResponse(updateInfo({ state: "running" }));
        // Панель перезапускается — соединение рвётся по сети.
        throw new TypeError("Failed to fetch");
      }),
    );
    vi.useFakeTimers();

    const { result } = renderHook(() => useUpdateInfo());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(result.current.info?.state).toBe("running");
    expect(result.current.restarting).toBe(false);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    // Состояние осталось прежним — запрос упал, а не сообщил об исходе.
    expect(result.current.info?.state).toBe("running");
    expect(result.current.restarting).toBe(true);
    expect(calls).toBeGreaterThan(1);
  });

  it("HTML-страница ошибки от прокси (502) во время перезапуска тоже не сбрасывает info", async () => {
    let calls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        calls += 1;
        if (calls === 1) return jsonResponse(updateInfo({ state: "running" }));
        return new Response("<html>502 Bad Gateway</html>", { status: 502 });
      }),
    );
    vi.useFakeTimers();

    const { result } = renderHook(() => useUpdateInfo());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(result.current.info?.state).toBe("running");
    expect(result.current.restarting).toBe(true);
  });

  it("панель снова отвечает удачным итогом — restarting гаснет, приходит итог", async () => {
    let calls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        calls += 1;
        if (calls === 1) return jsonResponse(updateInfo({ state: "running" }));
        if (calls === 2) throw new TypeError("Failed to fetch");
        return jsonResponse(
          updateInfo({
            state: "ok",
            state_at_utc: "2026-09-11T10:00:00Z",
            state_message: "обновление до 2.10.0 завершено",
          }),
        );
      }),
    );
    vi.useFakeTimers();

    const { result } = renderHook(() => useUpdateInfo());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000); // падает
    });
    expect(result.current.restarting).toBe(true);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000); // поднялась и ответила
    });
    expect(result.current.restarting).toBe(false);
    expect(result.current.timedOut).toBe(false);
    expect(result.current.info?.state).toBe("ok");
  });

  it("панель снова отвечает неудачей — итог показывает именно её, а не гадает", async () => {
    let calls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        calls += 1;
        if (calls === 1) return jsonResponse(updateInfo({ state: "running" }));
        if (calls === 2) throw new TypeError("Failed to fetch");
        return jsonResponse(
          updateInfo({
            state: "rolled-back",
            state_at_utc: "2026-09-11T10:00:00Z",
            state_message: "не удалось; сервер работает на 2.9.0",
          }),
        );
      }),
    );
    vi.useFakeTimers();

    const { result } = renderHook(() => useUpdateInfo());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(result.current.restarting).toBe(false);
    expect(result.current.info?.state).toBe("rolled-back");
  });

  it("не дождались панели за потолок ожидания — timedOut", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new TypeError("Failed to fetch");
      }),
    );
    vi.useFakeTimers();

    const { result } = renderHook(() => useUpdateInfo());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(result.current.restarting).toBe(true);
    expect(result.current.timedOut).toBe(false);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(PANEL_RESTART_TIMEOUT_MS + 3000);
    });
    expect(result.current.timedOut).toBe(true);
  });
});

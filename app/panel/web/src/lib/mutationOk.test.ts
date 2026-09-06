import { describe, expect, it, vi, beforeEach } from "vitest";
import { toast } from "sonner";

import { mutationOk } from "@/lib/api";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

// Отказ сервера — это ответ человеку. Панель его получала и выбрасывала:
// кнопка нажата, окно открыто, ничего не произошло и никто не объяснил почему
// (amnezia-vpn-server-o3hx).
describe("mutationOk", () => {
  beforeEach(() => {
    vi.mocked(toast.error).mockClear();
  });

  it("успех проходит молча", () => {
    expect(mutationOk({ ok: true })).toBe(true);
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("показывает причину отказа словами сервера", () => {
    expect(mutationOk({ ok: false, message: "MTU должен быть от 1280 до 1440" })).toBe(false);
    expect(toast.error).toHaveBeenCalledWith("MTU должен быть от 1280 до 1440");
  });

  it("не молчит и когда сервер не объяснил", () => {
    expect(mutationOk({ ok: false })).toBe(false);
    expect(toast.error).toHaveBeenCalledWith("Не удалось сохранить изменения");
  });

  it("не молчит и когда ответа вовсе нет", () => {
    expect(mutationOk(undefined)).toBe(false);
    expect(toast.error).toHaveBeenCalledTimes(1);
  });
});

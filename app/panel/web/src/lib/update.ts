import { useEffect, useState } from "react";

import { api, type UpdateInfo } from "@/lib/api";

/**
 * Загружает всё про обновление и обновляет, пока агент работает.
 *
 * Опрос, а не живой поток: обновление перезапускает саму панель, и отдавать
 * ход некому ровно в тот момент, когда он интереснее всего. Итог лежит в
 * файле и дождётся — в том числе закрытого браузера.
 */
export function useUpdateInfo() {
  const [info, setInfo] = useState<UpdateInfo | null>(null);

  async function reload() {
    try {
      const data = await api<UpdateInfo>("/api/update");
      setInfo(data ?? null);
    } catch {
      // Обновление перезапускает саму панель, и запрос в этот момент не
      // доходит. Это ожидаемая часть обновления, а не сбой: следующий опрос
      // попадёт уже в поднявшуюся панель. Прошлое известное состояние
      // остаётся на экране — оно всё ещё лучшее, что мы знаем.
    }
  }

  useEffect(() => {
    void reload();
  }, []);

  useEffect(() => {
    if (info?.state !== "running") return;
    // Панель в середине обновления перезапускается, и запрос падает. Это не
    // ошибка, а ожидаемая часть: следующий опрос попадёт в поднявшуюся.
    const timer = window.setInterval(() => void reload(), 3000);
    return () => window.clearInterval(timer);
  }, [info?.state]);

  return { info, reload };
}

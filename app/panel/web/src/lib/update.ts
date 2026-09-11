import { useEffect, useRef, useState } from "react";

import { api, type UpdateInfo } from "@/lib/api";

/**
 * Потолок ожидания, за которым мы честно говорим, что связь с панелью не
 * вернулась (amnezia-vpn-server-mrjh).
 *
 * Сам перезапуск контейнера панели занимает секунды, но шаг установки
 * сначала может тянуть свежий образ по сети сервера — а это на медленном
 * канале VPS способно занять заметно дольше. Три минуты покрывают
 * небыстрое скачивание и при этом не оставляют окно с полосой прогресса
 * крутиться бесконечно, если панель и правда не поднялась.
 */
export const PANEL_RESTART_TIMEOUT_MS = 3 * 60 * 1000;

// Ответом «панель на связи» считается только то, что по форме похоже на
// updateJSON с сервера. Прокси перед ещё не поднявшейся панелью может
// ответить вместо неё HTML-страницей ошибки — fetch при этом не падает,
// падает только разбор JSON внутри api(), и наружу это приходит как
// data === undefined. Такой ответ обязан считаться недоступностью панели, а
// не свежим состоянием обновления (amnezia-vpn-server-mrjh).
function isUpdateInfo(data: unknown): data is UpdateInfo {
  return !!data && typeof data === "object" && "state" in data;
}

/**
 * Загружает всё про обновление и обновляет, пока агент работает.
 *
 * Опрос, а не живой поток: обновление перезапускает саму панель, и отдавать
 * ход некому ровно в тот момент, когда он интереснее всего. Итог лежит в
 * файле и дождётся — в том числе закрытого браузера.
 *
 * ПОЧЕМУ НЕУДАЧНЫЙ ОПРОС НЕ ТРОГАЕТ info. Панель на шаге установки
 * перезапускает саму себя, и запрос в эту секунду либо падает по сети, либо
 * прокси перед ней отвечает вместо неё error-страницей. Это ожидаемая часть
 * обновления, а не признак его неудачи — единственный источник правды об
 * исходе лежит в update-state.json и придёт СЛЕДУЮЩИМ удачным ответом.
 * Поэтому последнее известное info (в т.ч. state === "running") остаётся на
 * экране нетронутым, а недоступность панели живёт отдельным состоянием
 * (amnezia-vpn-server-mrjh).
 */
export function useUpdateInfo() {
  const [info, setInfo] = useState<UpdateInfo | null>(null);
  // null — панель недавно отвечала; время — с которого перестала. Отдельно
  // от info, чтобы неудачный опрос не стирал последнее известное состояние
  // обновления (amnezia-vpn-server-mrjh).
  const [unreachableSince, setUnreachableSince] = useState<number | null>(null);
  const [timedOut, setTimedOut] = useState(false);
  // Источник правды для момента, с которого панель не отвечает. Ref, а не
  // чтение состояния из апдейтера setState: апдейтер обязан быть чистой
  // функцией — React волен вызвать его повторно (в StrictMode в разработке
  // так и делает), и вызов setTimedOut внутри него был бы побочным эффектом,
  // на который нельзя полагаться (amnezia-vpn-server-mrjh).
  const unreachableSinceRef = useRef<number | null>(null);

  async function reload() {
    try {
      const data = await api<UpdateInfo>("/api/update");
      if (!isUpdateInfo(data)) throw new Error("panel did not answer with update info");
      unreachableSinceRef.current = null;
      setInfo(data);
      setUnreachableSince(null);
      setTimedOut(false);
    } catch {
      // Один неудавшийся опрос не пишется поверх info (см. комментарий выше
      // функции) — фиксируем только сам факт и момент недоступности, чтобы
      // знать, когда упереться в потолок ожидания. Момент вычисляется здесь,
      // в самом обработчике, а не в апдейтере setState.
      const startedAt = unreachableSinceRef.current ?? Date.now();
      unreachableSinceRef.current = startedAt;
      setUnreachableSince(startedAt);
      if (Date.now() - startedAt >= PANEL_RESTART_TIMEOUT_MS) setTimedOut(true);
    }
  }

  useEffect(() => {
    void reload();
  }, []);

  useEffect(() => {
    // Стучимся, пока обновление идёт, и пока панель не отвечает — второе
    // само выключится, как только панель поднимется и пришлёт настоящее
    // состояние тем же опросом (amnezia-vpn-server-mrjh).
    if (info?.state !== "running" && unreachableSince === null) return;
    const timer = window.setInterval(() => void reload(), 3000);
    return () => window.clearInterval(timer);
  }, [info?.state, unreachableSince]);

  return {
    info,
    reload,
    /** Панель не ответила на последний опрос и мы ещё не сдались. */
    restarting: unreachableSince !== null,
    /** Не ответила дольше PANEL_RESTART_TIMEOUT_MS — ждать молча больше нечего. */
    timedOut,
  };
}

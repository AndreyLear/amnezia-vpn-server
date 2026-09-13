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
/**
 * Код страницы устарел, если сервер сообщает не ту установленную версию,
 * которую страница застала при загрузке (amnezia-vpn-server-e2ww).
 *
 * Панель обновляет сама себя, но открытая вкладка при этом не
 * перезагружается: в её памяти остаётся JS, загруженный ДО обновления. Номер
 * версии в «О версиях» при этом свежий — он приходит с сервера, — и этим
 * вводит в заблуждение: версия новая, поведение старое. Владелец три выпуска
 * подряд видел «правок нет», хотя они были в каждом.
 *
 * Сравнивается именно установленная версия, а не хеш бандла: она уже
 * приходит в этом опросе, отдельный запрос и разбор HTML не нужны, и ловит
 * любой путь обновления — из панели, из командной строки, из соседней
 * вкладки.
 */
export function isStalePage(bootInstalled: string | null, installed: string | undefined): boolean {
  return Boolean(bootInstalled && installed && installed !== bootInstalled);
}

export function useUpdateInfo(reloadPage: () => void = () => window.location.reload()) {
  const [info, setInfo] = useState<UpdateInfo | null>(null);
  // Версия сервера на момент загрузки этой страницы — то, с чем собран код,
  // который сейчас исполняется (amnezia-vpn-server-e2ww).
  const bootInstalledRef = useRef<string | null>(null);
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
      if (data.installed) {
        if (bootInstalledRef.current === null) {
          bootInstalledRef.current = data.installed;
        } else if (isStalePage(bootInstalledRef.current, data.installed)) {
          // Перезагрузка, а не перерисовка: устарел сам код, и никакое новое
          // состояние старый код не научит вести себя по-новому. Итог
          // обновления не теряется — отметка «увиден» ещё не стоит, и свежая
          // страница покажет его сама.
          reloadPage();
          return;
        }
      }
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
    // Вернулся на вкладку — спросить заново: сервер могли обновить из
    // командной строки или из другой вкладки, пока эта лежала в фоне и
    // ничего не опрашивала (amnezia-vpn-server-e2ww).
    const onVisible = () => {
      if (document.visibilityState === "visible") void reload();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => document.removeEventListener("visibilitychange", onVisible);
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

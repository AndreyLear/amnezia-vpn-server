import { useEffect, useRef, useState } from "react";

/**
 * Полоса хода обновления, не привязанная ко времени на сервере
 * (amnezia-vpn-server-mrjh).
 *
 * Панель в середине обновления перезапускается, и живой ход отдавать
 * некому. Полоса идёт к 99% и там замирает, дожидаясь настоящего итога из
 * файла состояния: иначе она соврала бы ровно в тот момент, когда
 * обновление затянулось, — а это единственный момент, когда на неё
 * смотрят.
 *
 * Девяносто девять процентов за пять минут — не обещание, а темп, при
 * котором полоса не упирается в потолок раньше обычного обновления (около
 * минуты) и не выглядит застывшей, если выпуск собирается на месте.
 */
const creepToPercent = 99;
const creepOverMs = 5 * 60 * 1000;
const creepTickMs = 1000;

/**
 * Общий счётчик хода для окна обновления и для тоста той же задачи
 * (amnezia-vpn-server-ekvi).
 *
 * Раньше это был приватный хук внутри UpdateDialog, включавшийся только
 * пока окно было открыто (`visible`): закрытое окно ничего не показывало, и
 * тикать было незачем. Теперь, пока обновление идёт, ход виден ВСЕГДА —
 * либо в открытом окне, либо в тосте, когда окно закрыто, — поэтому счётчик
 * поднят на уровень их общего родителя (UpdateBanner) и тикает всё время,
 * пока `running`. Если бы окно и тост каждый держали свой экземпляр этого
 * хука, у каждого был бы свой independent `startedAt`, и они разошлись бы в
 * показаниях в одну и ту же секунду — ровно то, чего просила избежать
 * задача.
 */
export function useCreepingProgress(running: boolean) {
  const [percent, setPercent] = useState(0);
  const startedAt = useRef<number | null>(null);

  useEffect(() => {
    if (!running) {
      startedAt.current = null;
      return;
    }
    startedAt.current ??= Date.now();
    const tick = () => {
      const started = startedAt.current;
      if (started === null) return;
      const share = Math.min(1, (Date.now() - started) / creepOverMs);
      setPercent(Math.round(share * creepToPercent));
    };
    tick();
    const timer = window.setInterval(tick, creepTickMs);
    return () => window.clearInterval(timer);
  }, [running]);

  return percent;
}

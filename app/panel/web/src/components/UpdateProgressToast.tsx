import { useEffect } from "react";
import { toast } from "sonner";

import { RunningEllipsis, UpdateProgressBar } from "@/components/UpdateDialog";

/**
 * Стабильный id: тот же id, переданный в toast.custom(), обновляет уже
 * показанную карточку вместо того, чтобы каждую секунду хода (см.
 * useCreepingProgress) класть в стек новый тост поверх старого
 * (amnezia-vpn-server-ekvi).
 */
const TOAST_ID = "amnezia-update-progress";

/**
 * Тост «идёт обновление» — то, что остаётся на экране, когда окно хода
 * обновления закрыто (amnezia-vpn-server-ekvi).
 *
 * Владелец: панель говорит человеку, что окно можно закрыть — обновление
 * идёт на сервере и не прервётся, — но стоит закрыть, и на экране не
 * остаётся ничего. Тост живёт до конца обновления и по нажатию открывает
 * окно обратно.
 *
 * ПОЧЕМУ toast.custom + duration: Infinity + один id. Обычный toast()
 * сонного sonner сам исчезает через несколько секунд — то, за чем следит
 * этот тост, не имеет заранее известной длины. toast.custom даёт
 * произвольную разметку (полосу хода, а не просто строку), duration:
 * Infinity отключает автозакрытие, а один и тот же id на каждый тик
 * обновляет уже показанную карточку вместо накопления дублей.
 *
 * ПЕРЕЖИВАЕТ ПЕРЕЗАПУСК ПАНЕЛИ. `restarting` приходит из useUpdateInfo и
 * означает лишь то, что последний опрос не дошёл, а не что обновление
 * сорвалось (см. lib/update.ts). Тост не гасится на этом — он гасится
 * только когда `visible` становится false: обновление кончилось, либо
 * открылось само окно и дублировать его незачем.
 */
export function UpdateProgressToast({
  visible,
  percent,
  restarting,
  onOpen,
}: {
  /**
   * running && !detailsOpen — открытое окно и так показывает ход, тост не
   * должен дублировать его (amnezia-vpn-server-ekvi).
   */
  visible: boolean;
  /**
   * Тот же процент, что видит открытое окно: общий счётчик из
   * useCreepingProgress, поднятый в UpdateBanner, а не отдельный экземпляр
   * здесь — иначе окно и тост разошлись бы в показаниях в одну и ту же
   * секунду (amnezia-vpn-server-ekvi).
   */
  percent: number;
  /** Панель не ответила на последний опрос — молчание, а не отказ (amnezia-vpn-server-mrjh). */
  restarting: boolean;
  onOpen: () => void;
}) {
  useEffect(() => {
    if (!visible) {
      toast.dismiss(TOAST_ID);
      return;
    }
    toast.custom(
      () => (
        <button
          type="button"
          onClick={onOpen}
          className="flex w-full flex-col gap-2 rounded-lg border border-border bg-popover p-4 text-left text-sm text-popover-foreground shadow-lg"
        >
          <p className="font-medium">
            Обновляем
            <RunningEllipsis />
          </p>
          <UpdateProgressBar percent={percent} />
          {restarting && <p className="text-muted-foreground">Панель перезапускается</p>}
        </button>
      ),
      { id: TOAST_ID, duration: Infinity },
    );
  }, [visible, percent, restarting, onOpen]);

  // Тост не должен пережить сам компонент — например, уход со страницы,
  // размонтировавший UpdateBanner.
  useEffect(() => {
    return () => {
      toast.dismiss(TOAST_ID);
    };
  }, []);

  return null;
}

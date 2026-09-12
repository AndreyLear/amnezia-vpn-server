import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { UpdateInfo } from "@/lib/api";

/**
 * Чем кончилось обновление — для холодного случая (amnezia-vpn-server-tjoq,
 * -mrjh).
 *
 * Показывается само, потому что обновление перезапускает саму панель:
 * браузер мог быть закрыт весь ход обновления (или окно UpdateDialog никто
 * не открывал), и если итог не показать — человек не узнает ничего. Итог
 * лежит на сервере, а не в этой вкладке, и дождётся.
 *
 * Пока открыт UpdateDialog — окно хода обновления, которое наблюдало за
 * этим самым обновлением и умеет само дойти до итога без перезагрузки, —
 * UpdateBanner не рендерит это окно вовсе: показывать один и тот же итог в
 * двух окнах разом незачем (amnezia-vpn-server-mrjh).
 *
 * Закрывается один раз и не возвращается: сервер запоминает, какой именно
 * итог показали. Следующее обновление кончится в другую секунду и покажется
 * само.
 */
// Экспортируется: UpdateDialog переиспользует те же подписи, когда сам
// показывает итог не отходя от окна хода обновления (amnezia-vpn-server-mrjh).
export const outcomes: Record<string, { title: string; fallback: string }> = {
  // "Завершено" read like a status report; the owner asked for the plain
  // fact of what happened to the panel (amnezia-vpn-server-jdkq).
  ok: { title: "Обновление установлено", fallback: "Сервер обновлён" },
  "rolled-back": {
    title: "Обновиться не удалось",
    fallback: "Сервер вернулся на прежний выпуск и работает",
  },
  failed: {
    title: "Обновление не удалось",
    fallback: "Сервер требует вмешательства",
  },
  refused: {
    title: "Обновление не начиналось",
    fallback: "Запрос отклонён",
  },
};

/** Состояния, на которых обновление кончилось — в одном месте на всю панель. */
export const finishedStates = Object.keys(outcomes);

/**
 * Text shown for a finished update. A successful outcome always names the
 * version from data (state_to, falling back to latest), never the host's
 * own sentence — "обновление до 2.10.15 завершено" reads like an agent log
 * line, and it duplicated the version the panel already knows on its own
 * (amnezia-vpn-server-jdkq). A failed outcome keeps the host's message
 * as-is: the wording and the circumstances there matter more than a fixed
 * template.
 */
export function outcomeMessage(info: UpdateInfo): string {
  const outcome = outcomes[info.state];
  if (info.state === "ok") {
    const version = info.state_to || info.latest;
    return version ? `Панель обновлена до версии ${version}` : (outcome?.fallback ?? "");
  }
  return info.state_message || outcome?.fallback || "";
}

export function UpdateOutcomeDialog({
  info,
  onAcknowledge,
}: {
  info: UpdateInfo | null;
  onAcknowledge: () => void;
}) {
  if (!info) return null;
  const outcome = outcomes[info.state];
  if (!outcome) return null;
  // Нечего показывать, если этот итог уже видели.
  if (!info.state_at_utc || info.state_at_utc === info.outcome_seen) return null;

  const failed = info.state !== "ok";

  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onAcknowledge())}>
      {/* No X: "Понятно" is the only way out, and it is the one action that
          also tells the server the outcome was seen — a close button would
          let someone dismiss the dialog without that happening
          (amnezia-vpn-server-jdkq). */}
      <DialogContent className="gap-6" showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{outcome.title}</DialogTitle>
        </DialogHeader>
        <p className={failed ? "text-destructive" : undefined}>{outcomeMessage(info)}</p>
        <DialogFooter>
          <Button type="button" onClick={onAcknowledge}>
            Понятно
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

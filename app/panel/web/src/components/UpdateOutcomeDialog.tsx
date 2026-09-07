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
 * Чем кончилось обновление (amnezia-vpn-server-tjoq).
 *
 * Показывается само, потому что обновление перезапускает саму панель:
 * человек нажал кнопку, страница перезагрузилась, и если итог не показать —
 * он не узнает ничего. Браузер при этом мог быть закрыт всё обновление, и
 * итог всё равно дождётся: он лежит на сервере, а не в этой вкладке.
 *
 * Закрывается один раз и не возвращается: сервер запоминает, какой именно
 * итог показали. Следующее обновление кончится в другую секунду и покажется
 * само.
 */
const outcomes: Record<string, { title: string; fallback: string }> = {
  ok: { title: "Обновление завершено", fallback: "Сервер обновлён" },
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
      <DialogContent className="gap-6">
        <DialogHeader>
          <DialogTitle>{outcome.title}</DialogTitle>
        </DialogHeader>
        <p className={failed ? "text-destructive" : undefined}>
          {info.state_message || outcome.fallback}
        </p>
        <DialogFooter>
          <Button type="button" onClick={onAcknowledge}>
            Понятно
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

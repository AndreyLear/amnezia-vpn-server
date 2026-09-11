import { useState } from "react";
import { XIcon } from "lucide-react";

import { UpdateDialog } from "@/components/UpdateDialog";
import { UpdateOutcomeDialog } from "@/components/UpdateOutcomeDialog";
import { Button } from "@/components/ui/button";
import { api, mutationOk, type MutationResponse, type UpdateInfo } from "@/lib/api";

/**
 * Полоса о новом выпуске (amnezia-vpn-server-tjoq).
 *
 * Появляется, когда есть что взять, и прячется крестиком до следующего
 * выпуска. Закрытие хранится на сервере, а не в браузере: владелец один и тот
 * же на компьютере и на телефоне, и закрыть полосу дважды — это лишняя работа
 * без причины.
 *
 * Значок-напоминание на кнопке меню при этом остаётся: он гаснет, когда
 * версия обновлена, а не когда полосу убрали с глаз.
 */
export function UpdateBanner({
  info,
  restarting = false,
  timedOut = false,
  onChanged,
}: {
  info: UpdateInfo | null;
  /** Панель не ответила на последний опрос — прокинуто в UpdateDialog. */
  restarting?: boolean;
  /** Не отвечает дольше потолка ожидания — прокинуто в UpdateDialog. */
  timedOut?: boolean;
  onChanged: () => void;
}) {
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [hiding, setHiding] = useState(false);

  async function dismiss() {
    if (!info || hiding) return;
    setHiding(true);
    const data = await api<MutationResponse>("/api/update/dismiss", {
      method: "POST",
      body: JSON.stringify({ version: info.latest }),
    });
    if (mutationOk(data)) onChanged();
    setHiding(false);
  }

  async function acknowledge() {
    if (!info) return;
    const data = await api<MutationResponse>("/api/update/dismiss", {
      method: "POST",
      body: JSON.stringify({ outcome: info.state_at_utc }),
    });
    if (mutationOk(data)) onChanged();
  }

  // Итог показывается всегда, а полоса о выпуске — только когда есть что
  // взять. Раньше здесь стоял общий ранний выход, и после удачного
  // обновления полоса исчезала вместе с окном ХОДА обновления: available
  // становилось false ровно в момент успеха, и то же самое условие гасило
  // UpdateDialog посреди работы — человек не узнавал, чем всё кончилось
  // (amnezia-vpn-server-tjoq, -mrjh). UpdateDialog теперь рендерится
  // отдельно от предложения обновиться, пока он открыт (detailsOpen) — так
  // available может стать false в любой момент, не закрывая окно, которое
  // всё ещё показывает пользователю ход или итог его собственного клика.
  //
  // Пока UpdateDialog открыт, он сам доведёт наблюдаемое обновление до
  // итога и закроется по «Понятно» — отдельный попап в этот момент молчит,
  // чтобы не показать тот же итог дважды в двух окнах (amnezia-vpn-server-mrjh).
  const outcome = detailsOpen ? null : (
    <UpdateOutcomeDialog info={info} onAcknowledge={() => void acknowledge()} />
  );
  const offer = info?.available && info.dismissed !== info.latest;

  return (
    <>
      {outcome}
      {offer ? (
        <div className="mt-4 flex items-center gap-3 rounded-lg border border-border bg-card px-4 py-3">
          <p className="min-w-0 flex-1">Вышла версия {info.latest}</p>
          <Button type="button" variant="outline" size="sm" onClick={() => setDetailsOpen(true)}>
            Показать подробности
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label="Скрыть до следующего выпуска"
            disabled={hiding}
            onClick={() => void dismiss()}
          >
            <XIcon />
          </Button>
        </div>
      ) : null}
      <UpdateDialog
        info={info}
        open={detailsOpen}
        onOpenChange={setDetailsOpen}
        onStarted={onChanged}
        restarting={restarting}
        timedOut={timedOut}
        onAcknowledge={() => void acknowledge()}
      />
    </>
  );
}

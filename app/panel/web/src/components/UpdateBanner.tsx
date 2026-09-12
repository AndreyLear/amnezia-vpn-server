import { useState } from "react";
import { XIcon } from "lucide-react";

import { UpdateDialog } from "@/components/UpdateDialog";
import { UpdateOutcomeDialog } from "@/components/UpdateOutcomeDialog";
import { UpdateProgressToast } from "@/components/UpdateProgressToast";
import { Button } from "@/components/ui/button";
import { api, mutationOk, type MutationResponse, type UpdateInfo } from "@/lib/api";
import { useCreepingProgress } from "@/lib/updateProgress";

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
 *
 * Пока обновление в состоянии "running" и окно его хода (UpdateDialog)
 * закрыто, здесь же рендерится UpdateProgressToast — иначе закрытое окно
 * оставляло бы экран без единого признака того, что что-то происходит
 * (amnezia-vpn-server-ekvi). Тост и окно делят один и тот же счётчик хода
 * (useCreepingProgress), поднятый сюда, чтобы не разойтись в показаниях.
 */
export function UpdateBanner({
  info,
  restarting = false,
  timedOut = false,
  onChanged,
  detailsOpen: controlledDetailsOpen,
  onDetailsOpenChange,
}: {
  info: UpdateInfo | null;
  /** Панель не ответила на последний опрос — прокинуто в UpdateDialog. */
  restarting?: boolean;
  /** Не отвечает дольше потолка ожидания — прокинуто в UpdateDialog. */
  timedOut?: boolean;
  // acknowledge() below awaits this, so the real reloadUpdate passed in from
  // HomePage (an async function) must be allowed to return its promise —
  // typing it as plain () => void would silently let a caller assume
  // "fire and forget" is fine (amnezia-vpn-server-jdkq).
  onChanged: () => void | Promise<void>;
  /**
   * Окно с описанием выпуска открывает не только плашка, но и пункт меню
   * «Доступна новая версия» (amnezia-vpn-server-919e). Поэтому состояние
   * можно поднять наверх; без этих пропов оно остаётся своим.
   */
  detailsOpen?: boolean;
  onDetailsOpenChange?: (open: boolean) => void;
}) {
  const [ownDetailsOpen, setOwnDetailsOpen] = useState(false);
  const detailsOpen = controlledDetailsOpen ?? ownDetailsOpen;
  const setDetailsOpen = (open: boolean) => {
    setOwnDetailsOpen(open);
    onDetailsOpenChange?.(open);
  };
  const [hiding, setHiding] = useState(false);
  // Какой итог человек уже закрыл в этом сеансе, отмеченный временем его
  // появления (amnezia-vpn-server-wbz0).
  //
  // Признак «увиден» живёт на сервере, и до правки jdkq окно закрывалось
  // раньше, чем отметка туда доезжала, — тогда второе окно с тем же итогом
  // успевало мелькнуть. Ожидание ответа это закрыло, но оставило условие на
  // времени: медленный ответ, обрыв сети, перезапуск панели посреди запроса
  // — и щель открывается снова. Здесь она закрыта по построению: закрытый
  // итог не показывается повторно, что бы ни ответил сервер и когда бы ни
  // обновился его ответ.
  const [seenOutcome, setSeenOutcome] = useState<string | null>(null);
  const running = info?.state === "running";
  // Один счётчик хода на окно и на тост (amnezia-vpn-server-ekvi): см.
  // комментарий в src/lib/updateProgress.ts про то, почему у него не может
  // быть двух независимых экземпляров.
  const percent = useCreepingProgress(running);

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

  // Returns whether the outcome is actually recorded as seen — both the
  // dismiss request AND the info reload after it must succeed. UpdateDialog
  // awaits this before closing itself, specifically so that the moment it
  // closes, info.outcome_seen already matches: closing on the dismiss
  // response alone (without waiting for the reload) would still leave a gap
  // where UpdateOutcomeDialog's own "already seen" check sees stale data
  // and flashes open (amnezia-vpn-server-jdkq). UpdateOutcomeDialog's own
  // "Понятно" doesn't need this care — it isn't racing a sibling popup — so
  // it still calls this fire-and-forget below.
  async function acknowledge(): Promise<boolean> {
    if (!info) return true;
    // Запоминается ДО запроса: смысл в том, чтобы итог не всплыл снова,
    // даже если запрос не дойдёт вовсе.
    if (info.state_at_utc) setSeenOutcome(info.state_at_utc);
    const data = await api<MutationResponse>("/api/update/dismiss", {
      method: "POST",
      body: JSON.stringify({ outcome: info.state_at_utc }),
    });
    if (!mutationOk(data)) return false;
    await onChanged();
    return true;
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
  const outcomeHandled = Boolean(info?.state_at_utc) && info?.state_at_utc === seenOutcome;
  const outcome =
    detailsOpen || outcomeHandled ? null : (
      <UpdateOutcomeDialog info={info} onAcknowledge={() => void acknowledge()} />
    );
  const offer = info?.available && info.dismissed !== info.latest;

  return (
    <>
      {outcome}
      {/* Виден, только пока окно хода обновления закрыто — иначе один и тот
          же ход показался бы дважды на экране разом (amnezia-vpn-server-ekvi). */}
      <UpdateProgressToast
        visible={running && !detailsOpen}
        percent={percent}
        restarting={restarting}
        onOpen={() => setDetailsOpen(true)}
      />
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
        onAcknowledge={acknowledge}
        percent={percent}
      />
    </>
  );
}

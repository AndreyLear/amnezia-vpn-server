import { useEffect, useState, type ReactNode } from "react";
import { toast } from "sonner";

import { RunningEllipsis } from "@/components/UpdateDialog";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api, mutationOk, type EntryHost, type EntryUpdate, type MutationResponse } from "@/lib/api";

/**
 * Обновление каскада (amnezia-vpn-server-q1do.2).
 *
 * Каскад сам догоняет версию панели: она приходит ему с ежеминутной
 * синхронизацией, он скачивает официальный выпуск, проверяет сумму и ставит,
 * а при неудаче возвращается на прежнюю версию и пробует снова не раньше чем
 * через полчаса. Здесь — что из этого вышло: полоса рядом с полосой о
 * выпуске этого сервера и кнопка «Обновить каскад», которая просит попытку
 * сразу, не дожидаясь паузы.
 *
 * Состояние приходит с опросом шапки (/api/stats/host, раз в пять секунд),
 * поэтому окно само доходит до итога, как UpdateDialog у этого сервера.
 * Итог показывается, только если окно было открыто: холодного случая, как
 * у UpdateOutcomeDialog, здесь нет — неудача остаётся в полосе, пока каскад
 * не догонит панель, а удача видна по тому, что полоса исчезла.
 *
 * Причина неудачи приходит от каскада кодом, а слова к ней — здесь: каскаду
 * не дают писать в окно владельца свой текст.
 */

// Тексты причин — после «Каскад entry не обновился: », с маленькой буквы.
const reasons: Record<string, string> = {
  download: "не удалось скачать выпуск с GitHub",
  checksum: "контрольная сумма выпуска не совпала",
  archive: "архив выпуска повреждён",
  install: "установка не удалась, каскад вернулся на прежнюю версию",
  inactive: "после установки каскад не запустился и вернулся на прежнюю версию",
  rollback: "установка не удалась, прежняя версия не вернулась — нужно зайти на каскад по ssh",
  interrupted: "обновление прервалось на середине",
  version: "не удалось сверить версию каскада с запрошенной",
};

export function entryUpdateReason(code: string): string {
  return reasons[code] ?? "причина неизвестна";
}

function capitalize(text: string): string {
  return text.charAt(0).toUpperCase() + text.slice(1);
}

/** Есть ли полосе что сказать про этот каскад. */
function entryNeedsAttention(update: EntryUpdate | undefined): update is EntryUpdate {
  if (!update) return false;
  return update.available || update.state === "updating";
}

type Phase = "offer" | "running" | "done" | "failed";

/**
 * Окно по кнопке «Обновить каскад»: подтверждение, ход, итог. Ход — пока
 * запрос ждёт каскада или каскад обновляется; итог — когда после нашего
 * запроса каскад догнал панель или сообщил о неудаче.
 */
export function EntryUpdateDialog({
  name,
  update,
  open,
  onOpenChange,
  onRequested,
}: {
  name: string;
  update: EntryUpdate;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRequested: () => void | Promise<void>;
}) {
  const [sending, setSending] = useState(false);
  // Запрос из этого окна отправлен, и после него каскад хоть раз был
  // в ходе (запрос ждёт или идёт обновление). Без второго условия окно
  // показало бы прежнюю неудачу как итог свежего запроса — до того, как
  // опрос принёс новое состояние.
  const [sent, setSent] = useState(false);
  const [seenProgress, setSeenProgress] = useState(false);
  const inProgress = update.requested || update.state === "updating";

  useEffect(() => {
    if (!open) {
      setSent(false);
      setSeenProgress(false);
      return;
    }
    if (sent && inProgress) setSeenProgress(true);
  }, [open, sent, inProgress]);

  let phase: Phase = "offer";
  if (sent) {
    if (!seenProgress || inProgress) phase = "running";
    else if (update.state === "failed") phase = "failed";
    else phase = "done";
  } else if (update.state === "updating") {
    phase = "running";
  } else if (!update.available) {
    // Окно открыли во время хода, и каскад за это время догнал панель:
    // предлагать «Обновить» уже нечего.
    phase = "done";
  }

  async function request() {
    if (sending) return;
    setSending(true);
    try {
      const data = await api<MutationResponse>(`/api/entries/${encodeURIComponent(name)}/update`, {
        method: "POST",
      });
      if (mutationOk(data)) {
        setSent(true);
        await onRequested();
      }
    } catch {
      toast.error("Не удалось запросить обновление каскада");
    } finally {
      setSending(false);
    }
  }

  const title =
    phase === "running" ? (
      <>
        Обновляем каскад {name}
        <RunningEllipsis />
      </>
    ) : phase === "done" ? (
      "Каскад обновлён"
    ) : phase === "failed" ? (
      `Каскад ${name} не обновился`
    ) : (
      `Обновить каскад ${name}`
    );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent bodyClassName="gap-6">
        <DialogHeader>
          <DialogTitle aria-label={phase === "running" ? `Обновляем каскад ${name}…` : undefined}>
            {title}
          </DialogTitle>
        </DialogHeader>
        {phase === "running" ? (
          <p className="text-sm text-muted-foreground">
            Обновление идёт на каскаде, а не в браузере. Окно можно закрыть
          </p>
        ) : phase === "done" ? (
          <p>
            Каскад {name} обновлён до версии {update.release || update.panel}
          </p>
        ) : phase === "failed" ? (
          <p className="text-destructive">{capitalize(entryUpdateReason(update.reason))}</p>
        ) : (
          <p className="text-sm text-muted-foreground">
            Каскад скачает выпуск {update.panel} с GitHub, проверит контрольную сумму и
            установит его. Клиенты, подключённые через каскад, будут без связи, пока идёт
            обновление. Обычно около минуты. Если установка не удастся, каскад вернётся на
            прежнюю версию
          </p>
        )}
        {phase === "running" ? null : phase === "offer" ? (
          <DialogFooter>
            <Button type="button" disabled={sending} onClick={() => void request()}>
              Обновить
            </Button>
          </DialogFooter>
        ) : (
          <DialogFooter>
            <Button type="button" onClick={() => onOpenChange(false)}>
              Понятно
            </Button>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  );
}

function stripText(name: string, u: EntryUpdate): ReactNode {
  if (!u.supported) {
    return `Каскад ${name} на версии ${u.release}, панель — на ${u.panel}. Обновите его один раз вручную по ssh — дальше он будет обновляться сам`;
  }
  if (u.state === "updating") {
    return (
      <>
        Каскад {name} обновляется до {u.target || u.panel}
        <RunningEllipsis />
      </>
    );
  }
  if (u.requested) return `Каскад ${name} начнёт обновление в течение минуты`;
  if (u.state === "failed") return `Каскад ${name} не обновился: ${entryUpdateReason(u.reason)}`;
  return `Каскад ${name} на версии ${u.release}, панель — на ${u.panel}`;
}

/**
 * Полосы про каскады, которым есть что сказать: отстаёт от панели или
 * обновляется. Стоит сразу под полосой о выпуске этого сервера.
 */
export function EntryUpdateBanner({
  entries,
  onChanged,
}: {
  entries: EntryHost[];
  onChanged: () => void | Promise<void>;
}) {
  const [openName, setOpenName] = useState<string | null>(null);
  const shown = entries.filter((e) => entryNeedsAttention(e.update));
  // Окно держится за свой каскад, даже когда полоса про него исчезла:
  // удача убирает полосу ровно в тот момент, когда окно показывает итог.
  const dialogEntry = entries.find((e) => e.name === openName);

  return (
    <>
      {shown.map((entry) => {
        const u = entry.update!;
        const canRequest = u.supported && u.available && u.state !== "updating" && !u.requested;
        return (
          <div
            key={entry.name}
            data-testid={`entry-update-${entry.name}`}
            className="mt-4 flex items-center gap-3 rounded-lg border border-border bg-card px-4 py-3"
          >
            <p className={u.state === "failed" && !u.requested ? "min-w-0 flex-1 text-destructive" : "min-w-0 flex-1"}>
              {stripText(entry.name, u)}
            </p>
            {canRequest ? (
              <Button type="button" variant="outline" size="sm" onClick={() => setOpenName(entry.name)}>
                Обновить каскад
              </Button>
            ) : null}
          </div>
        );
      })}
      {dialogEntry?.update ? (
        <EntryUpdateDialog
          name={dialogEntry.name}
          update={dialogEntry.update}
          open={openName !== null}
          onOpenChange={(open) => {
            if (!open) setOpenName(null);
          }}
          onRequested={onChanged}
        />
      ) : null}
    </>
  );
}

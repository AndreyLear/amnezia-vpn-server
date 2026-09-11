import { useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { outcomes } from "@/components/UpdateOutcomeDialog";
import { api, mutationOk, type MutationResponse, type UpdateInfo } from "@/lib/api";

/**
 * Окно изменений, ход обновления и — если это самое окно оставалось
 * открытым, когда обновление кончилось, — сам итог (amnezia-vpn-server-tjoq,
 * -mrjh).
 *
 * ПЕРЕЖИВАЕТ ПЕРЕЗАПУСК ПАНЕЛИ. Шаг установки перезапускает саму панель:
 * открытая вкладка на секунды-минуты теряет с ней связь, и опрос состояния
 * падает — по сети или потому что прокси перед ещё не поднявшейся панелью
 * ответил вместо неё error-страницей. Упавший запрос сам по себе — НЕ
 * признак неудачи обновления, это ожидаемая часть перезапуска; признак
 * неудачи только один — состояние из update-state.json, когда панель снова
 * ответила. Поэтому пока идёт "running", окно не закрывается и не сбрасывает
 * то, что знает, — оно говорит, что панель перезапускается, и продолжает
 * стучаться (см. useUpdateInfo в lib/update.ts). Если панель не поднимается
 * дольше PANEL_RESTART_TIMEOUT_MS, окно честно говорит, что связь не
 * вернулась, и предлагает обновить страницу — вместо вечно бегущей полосы.
 *
 * Когда панель отвечает снова и итог готов, это же окно показывает его сразу,
 * без перезагрузки страницы. UpdateOutcomeDialog в этот момент молчит (см.
 * UpdateBanner) — показывать один итог в двух окнах разом незачем.
 * UpdateOutcomeDialog остаётся отдельным окном для холодного случая: браузер
 * был закрыт весь ход обновления и это окно никто не открывал.
 *
 * ПОЛОСА ПРОГРЕССА НЕ ПРИВЯЗАНА КО ВРЕМЕНИ. Панель в середине обновления
 * перезапускается, и живой ход отдавать некому. Полоса идёт к 99% и там
 * замирает, дожидаясь настоящего итога из файла состояния: иначе она соврала
 * бы ровно в тот момент, когда обновление затянулось, — а это единственный
 * момент, когда на неё смотрят.
 *
 * Закрытый браузер ничего не ломает: итог лежит на сервере и дождётся.
 */

// Девяносто девять процентов за пять минут — не обещание, а темп, при котором
// полоса не упирается в потолок раньше обычного обновления (около минуты) и
// не выглядит застывшей, если выпуск собирается на месте.
const creepToPercent = 99;
const creepOverMs = 5 * 60 * 1000;
const creepTickMs = 1000;

// Отсчёт идёт, только пока на него смотрят: окно закрыто — таймер не нужен,
// а обновление от этого не останавливается. Настоящий итог всё равно придёт
// из файла состояния, а не отсюда.
function useCreepingProgress(running: boolean, visible: boolean) {
  const [percent, setPercent] = useState(0);
  const startedAt = useRef<number | null>(null);

  useEffect(() => {
    if (!running) {
      startedAt.current = null;
      return;
    }
    if (!visible) return;
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
  }, [running, visible]);

  return percent;
}

/**
 * Описание выпуска приходит текстом тела релиза с GitHub — тем же, что лежит
 * в RELEASES.md, где пункты записаны переносами по ширине. Физическая строка
 * там не равна пункту: один пункт занимает три-четыре строки, и отрисовка
 * «строка = абзац» превращала его в четыре абзаца с провалами между ними.
 * Владелец назвал это кашей и проблемой с интерлиньяжем — а это был не
 * межстрочный интервал, а межабзацный (amnezia-vpn-server-u1fv).
 */
type NotesBlock = { kind: "list"; items: string[] } | { kind: "text"; text: string };

// Дефис или звёздочка с пробелом — так GitHub записывает пункт. Всё
// остальное, что начинается без отступа, пунктом не является.
const bulletMark = /^[-*]\s+/;

/**
 * Складывает физические строки в логические куски.
 *
 * Пункт открывает строка с дефисом; строка с отступом продолжает открытый
 * пункт и приклеивается через пробел — это и есть перенос по ширине. Пустая
 * строка закрывает всё открытое. Строка без отступа и без дефиса — обычный
 * текст: в описании выпуска так пишут вступление и приписку, и списком они
 * притворяться не должны.
 */
function parseNotes(notes: string): NotesBlock[] {
  const blocks: NotesBlock[] = [];
  let open: { bullet: boolean; text: string } | null = null;

  // Строка дописывается в последний кусок на месте: собирать пункт в
  // отдельной переменной, а потом класть, значило бы держать два источника
  // правды об одном и том же пункте.
  const append = (text: string) => {
    const last = blocks[blocks.length - 1];
    if (last === undefined) return;
    if (last.kind === "list") last.items[last.items.length - 1] += " " + text;
    else last.text += " " + text;
  };

  for (const raw of notes.split("\n")) {
    const line = raw.trim();
    if (line === "") {
      open = null;
      continue;
    }
    const mark = bulletMark.exec(line);
    if (mark !== null) {
      const text = line.slice(mark[0].length);
      const last = blocks[blocks.length - 1];
      if (last !== undefined && last.kind === "list") last.items.push(text);
      else blocks.push({ kind: "list", items: [text] });
      open = { bullet: true, text };
      continue;
    }
    // Отступ продолжает то, что открыто; абзац продолжается и без отступа.
    // А пункт без отступа не продолжается: иначе приписка, набранная сразу
    // за списком, прилипла бы к последнему пункту.
    if (open !== null && (/^\s/.test(raw) || !open.bullet)) {
      append(line);
      open = { bullet: open.bullet, text: open.text + " " + line };
      continue;
    }
    blocks.push({ kind: "text", text: line });
    open = { bullet: false, text: line };
  }
  return blocks;
}

function Notes({ notes }: { notes: string }) {
  const blocks = parseNotes(notes);
  if (blocks.length === 0) {
    return <p className="text-muted-foreground">Описание выпуска не пришло</p>;
  }
  return (
    <div className="flex flex-col gap-2 text-sm">
      {blocks.map((block, index) =>
        block.kind === "list" ? (
          // Маркеры владелец попросил прямо. Список не flex: display:flex
          // отнял бы у пунктов display:list-item, а вместе с ним и маркеры.
          <ul key={index} className="list-disc space-y-2 pl-5">
            {block.items.map((item, itemIndex) => (
              <li key={itemIndex}>{item}</li>
            ))}
          </ul>
        ) : (
          <p key={index}>{block.text}</p>
        ),
      )}
    </div>
  );
}

export function UpdateDialog({
  info,
  open,
  onOpenChange,
  onStarted,
  restarting = false,
  timedOut = false,
  onAcknowledge = () => {},
}: {
  info: UpdateInfo | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onStarted: () => void;
  /** Панель не ответила на последний опрос — см. useUpdateInfo (amnezia-vpn-server-mrjh). */
  restarting?: boolean;
  /** Не отвечает дольше PANEL_RESTART_TIMEOUT_MS — ждать молча больше нечего. */
  timedOut?: boolean;
  /** Итог показан этим окном — отметить увиденным на сервере. */
  onAcknowledge?: () => void;
}) {
  const [starting, setStarting] = useState(false);
  const running = info?.state === "running";
  const percent = useCreepingProgress(running, open);

  // Итог показывается прямо здесь, если это самое окно и наблюдало за
  // обновлением: state — один из finishedStates и его ещё не видели
  // (state_at_utc отличается от outcome_seen — тот же признак, что и у
  // UpdateOutcomeDialog). Пустой state_at_utc — это НЕ «итог только что», а
  // «итога никогда не было»: свежая установка выглядит так же, и превращать
  // её в «итог» значило бы путать «когда-то кончилось» с «моё кончилось»
  // (amnezia-vpn-server-tjoq, -mrjh).
  const outcome = info ? outcomes[info.state] : undefined;
  const showOutcome = Boolean(outcome && info?.state_at_utc && info.state_at_utc !== info.outcome_seen);
  const outcomeFailed = info?.state !== "ok";

  async function start() {
    if (starting || running) return;
    setStarting(true);
    const data = await api<MutationResponse>("/api/update/start", { method: "POST" });
    if (mutationOk(data)) onStarted();
    setStarting(false);
  }

  function acknowledgeAndClose() {
    onAcknowledge();
    onOpenChange(false);
  }

  const title = running ? "Обновление идёт" : showOutcome ? outcome!.title : `Версия ${info?.latest ?? ""}`;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/*
        Стандартные sm:max-w-sm — это ~55 знаков в строке при text-sm, и
        собранный пункт описания выпуска в них ломается через слово.
        sm:max-w-lg даёт около семидесяти: столько же читается спокойно, но
        окно всё ещё окно, а не страница (amnezia-vpn-server-u1fv).
      */}
      <DialogContent className="gap-6 sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>

        {running ? (
          timedOut ? (
            // Потолок ожидания исчерпан: полоса, которая якобы всё ещё идёт
            // к цели, здесь соврала бы. Честнее сказать, что связи нет, и
            // отдать решение человеку, а не крутиться вечно
            // (amnezia-vpn-server-mrjh).
            <p className="text-sm text-destructive">
              Панель долго не отвечает. Обновление может всё ещё идти на сервере — обновите
              страницу, чтобы проверить
            </p>
          ) : (
            <div className="flex flex-col gap-2">
              <div
                role="progressbar"
                aria-valuenow={percent}
                aria-valuemin={0}
                aria-valuemax={100}
                className="h-2 w-full overflow-hidden rounded-full bg-muted"
              >
                <div
                  className="h-full rounded-full bg-primary transition-[width] duration-1000 ease-linear"
                  style={{ width: `${percent}%` }}
                />
              </div>
              <p className="text-sm text-muted-foreground">
                {restarting
                  ? // Панель на этом шаге перезапускает саму себя — молчание
                    // тут ожидаемо и не значит, что обновление сорвалось
                    // (amnezia-vpn-server-mrjh).
                    "Панель перезапускается — это ожидаемая часть обновления"
                  : (info?.state_step ? `Шаг: ${info.state_step}` : "Идёт обновление")}
              </p>
              <p className="text-sm text-muted-foreground">
                Окно можно закрыть — обновление от этого не остановится, а итог дождётся
              </p>
            </div>
          )
        ) : showOutcome ? (
          <p className={outcomeFailed ? "text-destructive" : undefined}>
            {info?.state_message || outcome!.fallback}
          </p>
        ) : (
          <>
            <Notes notes={info?.notes ?? ""} />
            <p className="text-sm text-muted-foreground">
              Пока идёт обновление, клиенты остаются без связи — обычно около минуты.
              Они восстановят её сами
            </p>
          </>
        )}

        {running ? (
          timedOut ? (
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => window.location.reload()}>
                Обновить страницу
              </Button>
            </DialogFooter>
          ) : null
        ) : showOutcome ? (
          <DialogFooter>
            <Button type="button" onClick={acknowledgeAndClose}>
              Понятно
            </Button>
          </DialogFooter>
        ) : (
          <DialogFooter>
            <Button type="button" disabled={starting} onClick={() => void start()}>
              Обновить
            </Button>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  );
}

import { useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api, mutationOk, type MutationResponse, type UpdateInfo } from "@/lib/api";

/**
 * Окно изменений и ход обновления (amnezia-vpn-server-tjoq).
 *
 * Чем обновление кончилось, рассказывает не это окно, а UpdateOutcomeDialog:
 * итог должен найти человека сам, в том числе после перезапуска панели,
 * который делает само обновление. Здесь же — то, что человек пришёл прочитать
 * ПЕРЕД тем, как нажать (amnezia-vpn-server-tjoq).
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
}: {
  info: UpdateInfo | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onStarted: () => void;
}) {
  const [starting, setStarting] = useState(false);
  const running = info?.state === "running";
  const percent = useCreepingProgress(running, open);

  async function start() {
    if (starting || running) return;
    setStarting(true);
    const data = await api<MutationResponse>("/api/update/start", { method: "POST" });
    if (mutationOk(data)) onStarted();
    setStarting(false);
  }

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
          <DialogTitle>{running ? "Обновление идёт" : `Версия ${info?.latest ?? ""}`}</DialogTitle>
        </DialogHeader>

        {running ? (
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
              {info?.state_step ? `Шаг: ${info.state_step}` : "Идёт обновление"}
            </p>
            <p className="text-sm text-muted-foreground">
              Окно можно закрыть — обновление от этого не остановится, а итог дождётся
            </p>
          </div>
        ) : (
          <>
            <Notes notes={info?.notes ?? ""} />
            <p className="text-sm text-muted-foreground">
              Пока идёт обновление, клиенты остаются без связи — обычно около минуты.
              Они восстановят её сами
            </p>
          </>
        )}

        {running ? null : (
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

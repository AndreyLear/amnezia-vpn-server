import { useRef, useState, type ReactNode } from "react";
import { MoreHorizontalIcon } from "lucide-react";
import { toast } from "sonner";

import { AboutDialog } from "@/components/AboutDialog";
import { AuditDialog } from "@/components/AuditDialog";
import { ServicesDialog } from "@/components/ServicesDialog";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { api, mutationOk, type MutationResponse, type UpdateInfo } from "@/lib/api";

/**
 * Меню в шапке (amnezia-vpn-server-8bt5).
 *
 * Пункты живут списком, а не вёрсткой: следующий добавляется строкой здесь и
 * ничего в шапке не трогает. Ничего существующего внутрь не переносим —
 * «Бэкап» частое действие, и убрать его вглубь меню было бы ухудшением, даже
 * если выглядит стройнее.
 */
type MenuItem = {
  id: string;
  label: ReactNode;
  onSelect: (event: Event) => void;
  ariaLabel?: string;
  disabled?: boolean;
  /** Пункт занят: вместо ожидания вслепую — вертушка на месте пункта. */
  busy?: boolean;
};

/**
 * Насколько часто спрашиваем и сколько ждём ответа (amnezia-vpn-server-n8w3).
 *
 * Хост ходит в GitHub с `curl --max-time 20`, и до этого ещё поднимается по
 * дорожке systemd. Потолок должен быть заметно больше, иначе панель объявит
 * молчание там, где ответ просто в пути; и не бесконечным, иначе вертушка
 * крутится вечно и врёт не меньше молчания.
 */
const CHECK_POLL_MS = 1000;
const CHECK_LIMIT_MS = 45000;

/**
 * Причины хост называет машинными словами (см. `write_check` в
 * update-check.sh). Человеку они советуют разное: до GitHub не достучались —
 * это про сеть сервера, ответ без выпуска — уже про нас.
 */
function failureText(reason: string): string {
  switch (reason) {
    case "unreachable":
      return "Не удалось связаться с GitHub";
    case "no-release":
      return "GitHub ответил, но выпуска в ответе нет";
    case "write-failed":
      return "Ответ получен, но сервер не смог его сохранить";
    default:
      return "Проверить обновления не удалось";
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

/** Три состояния одного и того же пункта меню, а не три разных пункта. */
type CheckState = "idle" | "checking" | "available";

const CHECK_LABEL_IDLE = "Проверить обновления";
const CHECK_LABEL_CHECKING = "Проверяю";
const CHECK_LABEL_AVAILABLE = "Доступна новая версия";

const CHECK_LABELS: Record<CheckState, string> = {
  idle: CHECK_LABEL_IDLE,
  checking: CHECK_LABEL_CHECKING,
  available: CHECK_LABEL_AVAILABLE,
};

/**
 * Подпись пункта «Проверить обновления».
 *
 * Рисуется ровно один, актуальный вариант — в отличие от подхода «наложить
 * все три друг на друга и прятать лишние через CSS», который выглядел бы
 * так же, но протёк бы в `textContent` и в доступное имя пункта даже у
 * скрытых вариантов. Вместо этого ширину под самый длинный вариант резервирует
 * `::before` в `.header-menu-check` (см. index.css): у генерируемого контента
 * псевдоэлемента нет текстового узла в DOM, поэтому в `textContent` он не
 * попадает (amnezia-vpn-server-3tm4).
 */
function CheckVariant({ state }: { state: CheckState }) {
  if (state === "checking") {
    return (
      <>
        {CHECK_LABEL_CHECKING}
        {/* Точки набегают по очереди (index.css, .animated-ellipsis): текст
            без движения читается как зависший, а не как идущая работа. */}
        <span className="animated-ellipsis" aria-hidden>
          <span>.</span>
          <span>.</span>
          <span>.</span>
        </span>
      </>
    );
  }
  if (state === "available") {
    return (
      <>
        {/* Переливание — то же, что у имени интерфейса в шапке
            (amnezia-vpn-server-nzb3): один приём — один сигнал. */}
        <span className="header-iface-shimmer">{CHECK_LABEL_AVAILABLE}</span>
        <span
          data-testid="update-item-badge"
          aria-hidden
          className="size-2 shrink-0 rounded-full bg-destructive"
        />
      </>
    );
  }
  return <>{CHECK_LABEL_IDLE}</>;
}

/**
 * Подпись пункта проверки обновлений (amnezia-vpn-server-3tm4, -nzb3).
 *
 * Ширину держат сами варианты, отрисованные друг поверх друга в одной
 * ячейке сетки: видно только текущий, остальные лежат под ним невидимыми.
 *
 * Первый заход резервировал место псевдоэлементом с самой длинной СТРОКОЙ
 * подписи — и ширину всё равно уводило, потому что в пункт входит не только
 * текст: у «Доступна новая версия» рядом бадж, у «Проверяю» — три точки.
 * Строка о них не знала (amnezia-vpn-server-x65u). Отрисованные варианты
 * знают: ширина ячейки равна самому широкому из них, чем бы он ни был набран.
 *
 * Скрытые варианты помечены aria-hidden, поэтому в доступное имя пункта
 * попадает только текущий. В textContent они попадают — это цена приёма, и
 * тесты поэтому спрашивают доступное имя, а не текст узла.
 */
function CheckMenuLabel({ state }: { state: CheckState }) {
  return (
    <span className="header-menu-check inline-grid">
      {(["idle", "checking", "available"] as const).map((variant) => (
        <span
          key={variant}
          aria-hidden={variant !== state}
          className={cn(
            "inline-flex items-center gap-1.5 whitespace-nowrap",
            variant !== state && "invisible",
          )}
        >
          <CheckVariant state={variant} />
        </span>
      ))}
    </span>
  );
}

export function HeaderMenu({
  pendingUpdate = false,
  onChecked,
  onShowUpdate,
}: {
  pendingUpdate?: boolean;
  /** Проверка принесла новый ответ: полосе о выпуске пора перечитать своё. */
  onChecked?: () => void;
  /** Показать окно с описанием выпуска (amnezia-vpn-server-919e). */
  onShowUpdate?: () => void;
}) {
  const [aboutOpen, setAboutOpen] = useState(false);
  const [servicesOpen, setServicesOpen] = useState(false);
  const [auditOpen, setAuditOpen] = useState(false);
  const [checking, setChecking] = useState(false);
  // Radix возвращает фокус на кнопку при закрытии меню. Для клавиатуры это
  // единственно верно; для мыши кольцо фокуса остаётся гореть, будто меню всё
  // ещё открыто (amnezia-vpn-server-c7iz).
  const openedByKeyboard = useRef(false);

  async function readUpdate(): Promise<UpdateInfo | null> {
    try {
      const data = await api<UpdateInfo>("/api/update");
      return data ?? null;
    } catch {
      // Один неудавшийся вопрос — не итог проверки: следующий в цикле
      // задастся через секунду и, скорее всего, попадёт.
      return null;
    }
  }

  /**
   * Ждёт, пока хост отчитается о новой проверке.
   *
   * Признак «готово» — не ответ на POST, а СМЕНА `checked_at_utc`: панель
   * наружу не ходит, она кладёт файл-просьбу в свой том, а GitHub спрашивает
   * хостовая служба и пишет ответ в update-check.json. Ответ на POST означает
   * лишь «просьба положена». Сравнивать результат со старым временем нельзя —
   * в файле лежит вчерашняя проверка, и она выглядит как удачная.
   */
  async function waitForCheck(before: string): Promise<UpdateInfo | null> {
    const deadline = Date.now() + CHECK_LIMIT_MS;
    // Цикл со сном, а не setInterval: следующий вопрос задаётся после ответа
    // на предыдущий, поэтому медленный сервер не копит очередь запросов.
    while (Date.now() < deadline) {
      await sleep(CHECK_POLL_MS);
      const info = await readUpdate();
      if (info?.checked_at_utc && info.checked_at_utc !== before) return info;
    }
    return null;
  }

  // Три исхода, и все три человек должен прочитать словами. Молчаливым был
  // ровно средний: выпуск не новее установленного не менял на экране ничего,
  // и нажавший не знал, сработала ли кнопка (amnezia-vpn-server-n8w3).
  function report(info: UpdateInfo) {
    if (info.check_result && info.check_result !== "ok") {
      toast.error(failureText(info.check_reason));
      return;
    }
    if (info.available && info.latest) {
      toast.success(`Вышла версия ${info.latest}`);
      return;
    }
    // Заголовок и текст — раздельно: sonner умеет двухчастный тост
    // (components/ui/sonner.tsx), и одна слипшаяся строка читалась хуже
    // (amnezia-vpn-server-3tm4).
    toast.success("Обновлений нет", { description: "Установлена последняя версия" });
  }

  async function checkForUpdates() {
    if (checking) return;
    setChecking(true);
    try {
      // Время прошлой проверки снимается ДО просьбы: только его смена
      // отличает свежий ответ от лежащего с прошлого раза.
      const before = (await readUpdate())?.checked_at_utc ?? "";
      const posted = await api<MutationResponse>("/api/update/check", { method: "POST" });
      // Проверку делает хост, и ответ появится в файле через секунду-другую.
      // Обещать результат немедленно значило бы соврать.
      if (!mutationOk(posted)) return;
      toast.success("Проверяю обновления");
      const info = await waitForCheck(before);
      if (!info) {
        // Честнее сказать, что ответа нет, чем выдать за ответ прошлую
        // проверку: тишина здесь означает, что хостовая служба не отработала.
        toast.error("Проверка не ответила. Попробуйте позже");
        return;
      }
      report(info);
      onChecked?.();
    } finally {
      setChecking(false);
    }
  }

  // Во время проверки — всегда «Проверяю…», независимо от того, что покажет
  // ответ; иначе «Доступна новая версия» — только когда проверка не идёт
  // (amnezia-vpn-server-nzb3).
  const checkState: CheckState = checking ? "checking" : pendingUpdate ? "available" : "idle";

  const items: MenuItem[] = [
    { id: "about", label: "О версиях", onSelect: () => setAboutOpen(true) },
    { id: "services", label: "Состояние служб", onSelect: () => setServicesOpen(true) },
    { id: "audit", label: "Журнал", onSelect: () => setAuditOpen(true) },
    {
      id: "check-updates",
      label: <CheckMenuLabel state={checkState} />,
      // Имя задаётся явно: варианты подписи лежат друг поверх друга ради
      // неизменной ширины, и хотя скрытые помечены aria-hidden, собирать имя
      // пункта из обрывков — лишний риск. Здесь оно всегда одно и то же
      // слово, что и на экране (amnezia-vpn-server-x65u).
      ariaLabel: CHECK_LABELS[checkState],
      busy: checking,
      disabled: checking,
      onSelect: (event) => {
        // Когда версия уже вышла, пункт так и называется — и должен её
        // показать, а не идти спрашивать GitHub заново про то, что и так
        // известно. Человек читает «Доступна новая версия» и ждёт, что ему
        // её покажут (amnezia-vpn-server-919e).
        if (pendingUpdate && onShowUpdate) {
          onShowUpdate();
          return;
        }
        // Меню не закрываем: пункт сам говорит «Проверяю…», и закрыться в
        // тот же миг значило бы снова оставить нажавшего ни с чем.
        event.preventDefault();
        void checkForUpdates();
      },
    },
  ];

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            type="button"
            variant="outline"
            size="icon"
            aria-label="Ещё"
            className="relative"
            onPointerDown={() => {
              openedByKeyboard.current = false;
            }}
            onKeyDown={(event) => {
              if (event.key === "Enter" || event.key === " ") {
                openedByKeyboard.current = true;
              }
            }}
          >
            <MoreHorizontalIcon />
            {pendingUpdate ? (
              // Напоминание о невзятом выпуске. Гаснет, когда версия
              // обновлена, — не когда полосу закрыли крестиком.
              <span
                data-testid="update-badge"
                aria-label="Вышел новый выпуск"
                className="absolute -end-0.5 -top-0.5 size-2 rounded-full bg-destructive ring-2 ring-background"
              />
            ) : null}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          // Ширина по содержимому, а не по кнопке. Кнопка меню — квадратная
          // иконка, и меню наследовало её ширину: названия ломались на две
          // строки, и меню выглядело случайным (amnezia-vpn-server-n8w3).
          className="w-auto min-w-max"
          onCloseAutoFocus={(event) => {
            if (!openedByKeyboard.current) {
              event.preventDefault();
            }
          }}
        >
          {items.map((item) => (
            <DropdownMenuItem
              key={item.id}
              className="whitespace-nowrap"
              disabled={item.disabled}
              aria-busy={item.busy}
              aria-label={item.ariaLabel}
              onSelect={item.onSelect}
            >
              {/* Вертушки рядом с подписью нет намеренно: она появлялась
                  только во время проверки и раздвигала меню на два десятка
                  пикселей — меню дёргалось под курсором ровно в тот момент,
                  когда по нему целятся. Что работа идёт, говорит сама
                  подпись «Проверяю…» с набегающим многоточием, а для тех,
                  кто не видит анимацию, остаётся aria-busy
                  (amnezia-vpn-server-x65u). */}
              {item.label}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
      <AboutDialog open={aboutOpen} onOpenChange={setAboutOpen} />
      <ServicesDialog open={servicesOpen} onOpenChange={setServicesOpen} />
      <AuditDialog open={auditOpen} onOpenChange={setAuditOpen} />
    </>
  );
}

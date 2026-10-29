import { Fragment, useEffect, useState, type ReactNode } from "react";

import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { UserText } from "@/components/UserText";
import { api, type EntryAbout, type Versions } from "@/lib/api";
import { formatDurationSeconds, formatHandshakeAge } from "@/lib/format";
import { cn } from "@/lib/utils";

/**
 * Окно «О версиях» (amnezia-vpn-server-8bt5).
 *
 * До него на вопрос «что у меня стоит» отвечал только ssh. Здесь всё, что
 * определяет поведение сервера: версии, уровень протокола, режим туннеля и
 * система, на которой это работает. Версия схемы SQLite сюда не выводится —
 * оператору она ничего не говорит, а сбой миграции панель показывает
 * отдельно; разработчику номер доступен через CLI.
 *
 * Неизвестное пишется словом «неизвестно», а не пустотой. Пустая строка
 * читается как «ничего нет», и это разные вещи: панель на развёртывании
 * старше этой возможности честно не знает, а не знает, что там ноль.
 */
// Standalone row values (dt/dd pair), so they get a capital letter like any
// other caption of this shape (amnezia-vpn-server-4cnf).
const unknown = "Неизвестно";

function text(value: string | undefined): string {
  const trimmed = value?.trim();
  return trimmed ? trimmed : unknown;
}

function flag(value: boolean | null | undefined): string {
  if (value === true) return "Включён";
  if (value === false) return "Выключен";
  return unknown;
}

// VLESS включён, когда Xray настроен: up, down и na — это ответил ли он на
// последний опрос, а строка говорит о настройке (то же состояние, что
// vless_state в шапке, amnezia-vpn-server-14y0.16).
function vlessFlag(state: string | undefined): string {
  if (!state) return unknown;
  return flag(state !== "none");
}

// Когда вход последний раз забирал конфиг. Связь решает сервер по своим
// часам (online), а «сколько назад» — тот же отсчёт, что у проверки служб.
function entrySync(entry: EntryAbout): string {
  if (!entry.online || !entry.last_sync_utc) return "Нет связи";
  return `${formatHandshakeAge(entry.last_sync_utc)} назад`;
}

// Сколько узел работает без перезагрузки — только из свежего отчёта
// (сервер иначе отдаёт null): устаревший аптайм врёт.
function entryUptime(entry: EntryAbout): string {
  if (entry.uptime_seconds == null) return unknown;
  return formatDurationSeconds(entry.uptime_seconds);
}

// Самообновление каскада (amnezia-vpn-server-q1do.2): строка есть, только
// пока есть что сказать. Причины здесь нет — она в полосе на главной, а
// длинная сломала бы строку списка.
function entryUpdateState(entry: EntryAbout): string | null {
  const u = entry.update;
  if (!u) return null;
  if (u.state === "updating") return `Обновляется до ${u.target || u.panel}`;
  if (u.requested) return "Запрошено";
  if (u.state === "failed" && u.available) return "Не удалось";
  return null;
}

// Подпись строки каскада: с именем владельца — «Каскад «Москва»», без —
// как раньше, по узлу (amnezia-vpn-server-q1do.4). Имя помечено как
// непереводимое, как имена клиентов.
function entryCaption(entry: EntryAbout): ReactNode {
  const title = entry.title?.trim();
  if (!title) return `Каскад ${entry.name}`;
  return (
    <>
      Каскад «<UserText>{title}</UserText>»
    </>
  );
}

function Row({
  label,
  value,
  nested = false,
}: {
  label: ReactNode;
  value: ReactNode;
  nested?: boolean;
}) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-2">
      <dt className={cn("shrink-0 text-muted-foreground", nested && "ps-4")}>{label}</dt>
      <dd className="min-w-0 text-end break-words">{value}</dd>
    </div>
  );
}

export function AboutDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [versions, setVersions] = useState<Versions | null>(null);

  useEffect(() => {
    if (!open) return;
    let alive = true;
    void api<Versions>("/api/versions").then((data) => {
      if (alive) setVersions(data ?? null);
    });
    return () => {
      alive = false;
    };
  }, [open]);

  // Приписка нужна только когда есть что сказать: «вышла новее» — новость,
  // а «— последняя» повторяло то, что и так видно по отсутствию новости, и
  // читалось как вторая, незнакомая версия (amnezia-vpn-server-cavu).
  const product = (() => {
    const installed = text(versions?.product);
    if (installed === unknown) return unknown;
    if (versions?.latest && versions.latest !== versions.product) {
      return `${installed} — вышла ${versions.latest}`;
    }
    return installed;
  })();

  const entries = versions?.entries ?? [];
  const serverName = versions?.server_name?.trim() ?? "";
  const vlessState = versions?.vless_state;
  const showXray = (vlessState !== undefined && vlessState !== "none") || entries.length > 0;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent bodyClassName="gap-6">
        <DialogHeader>
          <DialogTitle>О версиях</DialogTitle>
        </DialogHeader>
        <dl className="divide-y divide-border text-sm">
          {/* Имя сервера от владельца (amnezia-vpn-server-q1do.4): строка
              есть, только когда имя задано, — без него окно как раньше. */}
          {serverName ? <Row label="Имя" value={<UserText>{serverName}</UserText>} /> : null}
          <Row label="Версия" value={product} />
          <Row label="Протокол" value={text(versions?.protocol)} />
          {/* Версия того, на чём туннель работает на самом деле: модуль ядра на
              хосте или amneziawg-go из образа. Показывать amneziawg-go, когда
              туннель на модуле, значило бы называть версию того, что не
              используется (amnezia-vpn-server-y7bp). Неизвестно — как раньше. */}
          {versions?.tunnel_driver === "kernel" ? (
            <Row label="Модуль ядра" value={text(versions.amneziawg_module)} />
          ) : (
            <Row label="amneziawg-go" value={text(versions?.amneziawg_go)} />
          )}
          <Row label="amneziawg-tools" value={text(versions?.amneziawg_tools)} />
          {/* Xray (amnezia-vpn-server-14y0.16): на установке только с AWG
              строк нет вовсе, окно выглядит как раньше. Пока ответа нет,
              их тоже нет — vless_state ещё не пришёл. Узел подписан
              «Каскад», как маршрут в окне клиента (14y0.17): одно понятие —
              одно слово. */}
          {showXray && (
            <>
              <Row label="Xray" value={text(versions?.xray)} />
              <Row label="VLESS" value={vlessFlag(versions?.vless_state)} />
              {/* Под строкой узла — его версия выпуска, Xray и время без
                  перезагрузки из отчёта узла (amnezia-vpn-server-q1do.1).
                  Подписи без имени и с отступом: они стоят прямо под своим
                  узлом, а отступ не даёт спутать «Xray» узла с «Xray»
                  этого сервера выше. Версии — последние известные и при
                  «Нет связи»: по ним видно, что обновлять. */}
              {entries.map((entry) => (
                <Fragment key={entry.name}>
                  <Row label={entryCaption(entry)} value={entrySync(entry)} />
                  <Row nested label="Версия" value={text(entry.release)} />
                  <Row nested label="Xray" value={text(entry.xray)} />
                  <Row nested label="Без перезагрузки" value={entryUptime(entry)} />
                  {entryUpdateState(entry) !== null && (
                    <Row nested label="Обновление" value={entryUpdateState(entry)!} />
                  )}
                </Fragment>
              ))}
            </>
          )}
          <Row label="IPv6 в туннеле" value={flag(versions?.tunnel_ipv6)} />
          <Row label="Резолвер в туннеле" value={flag(versions?.tunnel_dns)} />
          <Row label="Сторож" value={flag(versions?.watchdog)} />
          <Row label="Защита SSH" value={flag(versions?.fail2ban)} />
          <Row label="Проверка обновлений" value={flag(versions?.update_check)} />
          <Row label="Система" value={text(versions?.os)} />
          <Row label="Docker" value={text(versions?.docker)} />
        </dl>
      </DialogContent>
    </Dialog>
  );
}

import { useEffect, useState } from "react";

import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api, type Versions } from "@/lib/api";

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

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-2">
      <dt className="shrink-0 text-muted-foreground">{label}</dt>
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

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="gap-6">
        <DialogHeader>
          <DialogTitle>О версиях</DialogTitle>
        </DialogHeader>
        <dl className="divide-y divide-border text-sm">
          <Row label="Версия" value={product} />
          <Row label="Протокол" value={text(versions?.protocol)} />
          <Row label="amneziawg-go" value={text(versions?.amneziawg_go)} />
          <Row label="amneziawg-tools" value={text(versions?.amneziawg_tools)} />
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

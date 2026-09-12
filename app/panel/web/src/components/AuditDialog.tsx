import { useEffect, useState } from "react";

import { UserText } from "@/components/UserText";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api, type AuditEntry } from "@/lib/api";
import { formatHandshake } from "@/lib/format";

/**
 * Окно «Журнал» (amnezia-vpn-server-gqep).
 *
 * Панель меняет то, что видит пользователь: клиентов включают, отключают,
 * переименовывают, меняют им MTU. Ничего из этого не записывалось — ни кто,
 * ни когда. При одном администраторе это прежде всего защита от «я такого не
 * делал»: с записью видно, было действие или не было.
 *
 * Секретов здесь нет и не будет: ни ключей, ни введённых паролей.
 */
// Capitalized at render time, not in storage: these are the SPA's own
// display strings for the action codes stored in the audit log, never the
// stored rows themselves, so recasing them here does not rewrite history
// (amnezia-vpn-server-4cnf: the owner's screenshots showed lowercase entry
// titles like "вход" and "клиент изменён").
const actions: Record<string, string> = {
  login: "Вход",
  "login.failed": "Неудачный вход",
  logout: "Выход",
  "client.add": "Клиент добавлен",
  "client.edit": "Клиент изменён",
  "client.mtu": "MTU клиента",
  "client.rate": "Ограничение скорости",
  "client.toggle": "Клиент",
  "client.delete": "Клиент удалён",
  "backup.restore": "Восстановление из копии",
};

function actionTitle(action: string): string {
  return actions[action] ?? action;
}

function Entry({ entry }: { entry: AuditEntry }) {
  const failed = entry.action === "login.failed";
  return (
    <div className="flex flex-col gap-0.5 py-2">
      <div className="flex items-baseline justify-between gap-3">
        <span className={failed ? "text-destructive" : undefined}>
          {actionTitle(entry.action)}
          {entry.subject ? (
            <>
              {" "}
              <UserText>{entry.subject}</UserText>
            </>
          ) : null}
        </span>
        <span className="shrink-0 text-sm text-muted-foreground">
          {formatHandshake(entry.at_utc)}
        </span>
      </div>
      {/* Подробность — отдельной строкой, а не хвостом заголовка: слитая
          фраза ломалась посередине на узких окнах (amnezia-vpn-server-4yo4). */}
      {entry.detail ? (
        <span className="text-sm text-muted-foreground">{entry.detail}</span>
      ) : null}
      {entry.actor ? (
        <span className="text-sm text-muted-foreground">
          <UserText>{entry.actor}</UserText>
        </span>
      ) : null}
    </div>
  );
}

export function AuditDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [entries, setEntries] = useState<AuditEntry[] | null>(null);

  useEffect(() => {
    if (!open) return;
    let alive = true;
    void api<{ entries: AuditEntry[] }>("/api/audit")
      .then((data) => {
        if (alive) setEntries(data?.entries ?? []);
      })
      .catch(() => {
        if (alive) setEntries([]);
      });
    return () => {
      alive = false;
    };
  }, [open]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/* Шире стандартного диалога (sm:max-w-sm): заголовок записи и время
          в одной строке — «ограничение скорости router test» плюс полная
          метка времени — переносились посреди фразы. sm:max-w-md — та же
          ширина, что у ClientInfoDialog и BackupUploadDialog, и её хватает
          с запасом (amnezia-vpn-server-4yo4). */}
      <DialogContent className="gap-6 sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Журнал</DialogTitle>
        </DialogHeader>
        {entries === null ? null : entries.length === 0 ? (
          <p className="text-muted-foreground">Записей пока нет</p>
        ) : (
          // Список ограничен по высоте и прокручивается внутри себя: окно
          // на две сотни записей уехало бы за край экрана целиком.
          <div className="max-h-[60vh] divide-y divide-border overflow-y-auto">
            {entries.map((entry, index) => (
              <Entry key={`${entry.at_utc}-${index}`} entry={entry} />
            ))}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

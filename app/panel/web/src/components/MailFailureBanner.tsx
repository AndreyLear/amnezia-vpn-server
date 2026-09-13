import { useCallback, useEffect, useState } from "react";

import { NotificationsDialog } from "@/components/NotificationsDialog";
import { Button } from "@/components/ui/button";
import { api, type MailInfo } from "@/lib/api";
import { formatHandshake } from "@/lib/format";

/**
 * Полоса «письма не уходят» (amnezia-vpn-server-pz2r).
 *
 * Молчащий канал уведомлений хуже отсутствующего: человек думает, что его
 * предупредят, а его не предупредят. Поэтому отказ показывается здесь, над
 * списком клиентов, а не только в окне «Уведомления», куда никто не заходит
 * без причины.
 *
 * Крестика нет намеренно: полоса уходит сама, когда письмо снова дошло.
 * «Почта не настроена» — не ошибка, и полосы в этом случае нет.
 */

/** Как часто панель спрашивает о состоянии писем, пока открыта. */
const POLL_MS = 60_000;

function describe(
  info: MailInfo,
): { title: string; detail?: string; serverReply?: string } | null {
  if (info.channel === "password_missing") {
    return {
      title: "Письма о сбоях не уходят",
      detail: "После восстановления из бэкапа нужно заново ввести пароль почты",
    };
  }
  if (info.channel !== "failing") return null;
  const failure = info.last_failure;
  const testAt = info.test.state === "failed" ? info.test.at_utc : undefined;
  // Из двух отказов показываем поздний: о нём и речь в channel.
  if (failure && (!testAt || failure.at_utc >= testAt)) {
    return {
      title: "Письма о сбоях не уходят",
      detail: `«${failure.subject}» не отправлено ${formatHandshake(failure.at_utc)}`,
      serverReply: failure.error,
    };
  }
  return {
    title: "Письма о сбоях не уходят",
    detail: `Пробное письмо не ушло ${formatHandshake(testAt ?? null)}`,
    serverReply: info.test.error,
  };
}

export function MailFailureBanner() {
  const [info, setInfo] = useState<MailInfo | null>(null);
  const [open, setOpen] = useState(false);

  const load = useCallback(async () => {
    const next = await api<MailInfo>("/api/mail").catch(() => null);
    if (next && typeof next === "object" && "channel" in next) setInfo(next);
  }, []);

  useEffect(() => {
    void load();
    const timer = window.setInterval(() => void load(), POLL_MS);
    const onVisible = () => {
      if (document.visibilityState === "visible") void load();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [load]);

  const problem = info ? describe(info) : null;

  return (
    <>
      {problem ? (
        <div
          role="alert"
          className="mt-4 flex items-center gap-3 rounded-lg border border-destructive/40 bg-card px-4 py-3 max-sm:flex-col max-sm:items-stretch"
        >
          <div className="grid min-w-0 flex-1 gap-1">
            <p className="text-destructive">{problem.title}</p>
            {problem.detail ? (
              <p className="break-words text-sm text-muted-foreground">{problem.detail}</p>
            ) : null}
            {problem.serverReply ? (
              <p className="break-words font-mono text-xs text-muted-foreground">{problem.serverReply}</p>
            ) : null}
          </div>
          <Button type="button" variant="outline" size="sm" onClick={() => setOpen(true)}>
            Открыть настройки
          </Button>
        </div>
      ) : null}
      <NotificationsDialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          if (!next) void load();
        }}
      />
    </>
  );
}

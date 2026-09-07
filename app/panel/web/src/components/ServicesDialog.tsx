import { useEffect, useState } from "react";

import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api, type ServiceState, type ServicesInfo } from "@/lib/api";
import { formatHandshake, formatHandshakeAge } from "@/lib/format";

/**
 * Окно «Состояние служб» (amnezia-vpn-server-eq82).
 *
 * Отказ, который чинится сам, невидим: сторож каждую минуту проверяет
 * резолвер и туннель и перезапускает то, что перестало работать, — но узнать
 * об этом можно было только по ssh, и владелец слышал о сбое от
 * пользователей. Ровно та беда, из-за которой сторож и заводился.
 *
 * Здесь нет действий и не задумано: это правда о состоянии, а не пульт.
 */
const titles: Record<string, string> = {
  dns: "Резолвер в туннеле",
  awg: "Туннель",
};

function serviceTitle(name: string): string {
  return titles[name] ?? name;
}

function verdict(service: ServiceState): string {
  if (service.state === "ok") return "работает";
  if (service.state === "fail") return service.reason || "не отвечает";
  return "неизвестно";
}

function Row({ service }: { service: ServiceState }) {
  const broken = service.state === "fail";
  return (
    <div className="flex flex-col gap-1 py-3">
      <div className="flex items-baseline justify-between gap-4">
        <span className="shrink-0 text-muted-foreground">{serviceTitle(service.name)}</span>
        <span className={broken ? "text-end text-destructive" : "text-end"}>
          {verdict(service)}
        </span>
      </div>
      {/* След перезапуска остаётся и после починки: через минуту всё
          исправно, и без этой строки владелец не узнал бы, что сервер сам
          себя чинил. */}
      {service.restarted_at_utc ? (
        <p className="text-sm text-muted-foreground">
          Перезапускался {formatHandshake(service.restarted_at_utc)}
          {service.restart_reason ? ` — ${service.restart_reason}` : ""}
        </p>
      ) : null}
    </div>
  );
}

export function ServicesDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [info, setInfo] = useState<ServicesInfo | null>(null);

  useEffect(() => {
    if (!open) return;
    let alive = true;
    void api<ServicesInfo>("/api/services")
      .then((data) => {
        if (alive) setInfo(data ?? null);
      })
      .catch(() => {
        // Пустое окно честнее выдуманного состояния.
      });
    return () => {
      alive = false;
    };
  }, [open]);

  const off = info?.watchdog === false;
  const services = info?.services ?? [];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="gap-6">
        <DialogHeader>
          <DialogTitle>Состояние служб</DialogTitle>
        </DialogHeader>
        {off ? (
          // Сервер без сторожа не сломан — за ним просто никто не следит.
          <p className="text-muted-foreground">
            Сторож не установлен, так что проверять состояние некому
          </p>
        ) : services.length === 0 ? (
          <p className="text-muted-foreground">
            Сторож ещё не проверял службы — загляните через минуту
          </p>
        ) : (
          <>
            <div className="divide-y divide-border text-sm">
              {services.map((service) => (
                <Row key={service.name} service={service} />
              ))}
            </div>
            {info?.checked_at_utc ? (
              <p className="text-sm text-muted-foreground">
                Проверено {formatHandshakeAge(info.checked_at_utc)} назад
              </p>
            ) : null}
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

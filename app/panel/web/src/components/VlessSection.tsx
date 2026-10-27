import { useEffect, useState, type ReactNode } from "react";
import { Copy, QrCode } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { vlessQrUrl, type VlessInfo, type VlessRoute } from "@/lib/api";
import { formatBytes, formatHandshake } from "@/lib/format";

// Раздел VLESS в окне клиента (amnezia-vpn-server-14y0.5): второй способ
// подключения рядом с AmneziaWG. На мобильном интернете в РФ AmneziaWG
// режется, а VLESS+XHTTP+Reality на 443 проходит — напрямую или через
// российский узел-вход. Тексты согласованы с редактором.
//
// С amnezia-vpn-server-14y0.8 раздел — содержимое вкладки «VLESS»: кроме
// ссылок в нём трафик VLESS и последняя активность. Панель считает их по
// счётчикам Xray только для подключений напрямую: через вход трафик
// приходит одной служебной учёткой, и по клиентам его не разложить —
// об этом оговорка под полями, когда входы есть.

export function vlessRouteLabel(route: VlessRoute): string {
  if (!route.entry) return "Напрямую";
  return `Через вход ${route.prefix.trim() || route.entry}`;
}

function vlessRouteHint(route: VlessRoute): string {
  return route.entry
    ? "Для мобильного интернета, где напрямую не подключается. Российские сайты открываются мимо VPN."
    : "Подходит для Wi-Fi и домашнего интернета, на мобильном работает не всегда";
}

type VlessSectionProps = {
  clientId: number;
  clientName: string;
  // Включённость — ключ перезагрузки: после «Отключить» ссылки должны
  // исчезнуть, после «Включить» — появиться, без повторного открытия окна.
  enabled: boolean;
  load: (id: number) => Promise<VlessInfo>;
  // Трафик VLESS напрямую с точки зрения клиента и время последнего
  // движения (null — не было).
  traffic: { up: number; down: number; lastActive: string | null };
};

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid gap-0.5 py-2">
      <dt className="text-muted-foreground">{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}

type State =
  | { kind: "loading" }
  | { kind: "error" }
  | { kind: "ready"; info: VlessInfo };

export function VlessSection({ clientId, clientName, enabled, load, traffic }: VlessSectionProps) {
  const [state, setState] = useState<State>({ kind: "loading" });
  const [qrRoute, setQrRoute] = useState<VlessRoute | null>(null);

  useEffect(() => {
    let cancelled = false;
    setState({ kind: "loading" });
    load(clientId).then(
      (info) => {
        if (!cancelled) setState({ kind: "ready", info });
      },
      () => {
        if (!cancelled) setState({ kind: "error" });
      },
    );
    return () => {
      cancelled = true;
    };
  }, [clientId, enabled, load]);

  async function copy(route: VlessRoute) {
    try {
      // Без https браузер может не дать буфер обмена вовсе — тогда
      // запасной путь QR, о нём и говорит тост.
      if (!navigator.clipboard) throw new Error("no clipboard");
      await navigator.clipboard.writeText(route.link);
      toast.success("Ссылка скопирована");
    } catch {
      toast.error("Не удалось скопировать ссылку, откройте QR");
    }
  }

  const dlClass = "grid divide-y divide-border gap-0 text-sm";
  if (state.kind === "loading") return null;
  if (state.kind === "error") {
    return (
      <dl className={dlClass} data-testid="vless-section">
        <div className="grid gap-0.5 py-2">
          <dt className="sr-only">Маршруты</dt>
          <dd className="text-sm text-destructive">
            Не удалось загрузить ссылки VLESS, откройте окно клиента заново
          </dd>
        </div>
      </dl>
    );
  }
  const { info } = state;
  // Xray на сервере не настроен и узлов нет — ни ссылок, ни трафика.
  if (info.enabled && info.routes.length === 0) {
    return (
      <dl className={dlClass} data-testid="vless-section">
        <div className="grid gap-0.5 py-2">
          <dt className="sr-only">Маршруты</dt>
          <dd className="text-sm text-muted-foreground">VLESS на сервере не настроен</dd>
        </div>
      </dl>
    );
  }
  const hasEntry = info.routes.some((route) => route.entry);
  const usage = (
    <>
      <Field label="Трафик">
        ↓ {formatBytes(traffic.down)} · ↑ {formatBytes(traffic.up)}
      </Field>
      <Field label="Последняя активность">{formatHandshake(traffic.lastActive)}</Field>
    </>
  );
  if (!info.enabled) {
    return (
      <dl className={dlClass} data-testid="vless-section">
        <div className="grid gap-0.5 py-2">
          <dt className="sr-only">Маршруты</dt>
          <dd className="text-sm text-muted-foreground">
            Клиент на паузе — ссылки VLESS появятся после включения
          </dd>
        </div>
        {usage}
      </dl>
    );
  }

  return (
    <div className="grid gap-2" data-testid="vless-section">
      <dl className={dlClass}>
        <div className="grid gap-1 py-2">
          <dt className="sr-only">Маршруты</dt>
          {info.routes.map((route) => {
            const label = vlessRouteLabel(route);
            return (
              <dd key={route.entry || "direct"} className="flex items-center gap-2 py-1">
                <div className="min-w-0 flex-1">
                  <div>{label}</div>
                  <div className="text-sm text-muted-foreground">{vlessRouteHint(route)}</div>
                </div>
                <Button
                  type="button"
                  variant="outline"
                  aria-label={`Скопировать ссылку: ${label}`}
                  onClick={() => void copy(route)}
                >
                  <Copy data-icon="inline-start" aria-hidden />
                  Ссылка
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  aria-label={`QR: ${label}`}
                  onClick={() => setQrRoute(route)}
                >
                  <QrCode data-icon="inline-start" aria-hidden />
                  QR
                </Button>
              </dd>
            );
          })}
        </div>
        {usage}
      </dl>
      {/* Оговорка касается обоих полей: подключение через вход не
          отмечается ни трафиком, ни активностью. Без входов её нет. */}
      {hasEntry ? (
        <p className="text-sm text-muted-foreground">
          Показан только трафик напрямую. Трафик через вход пока не учитывается
        </p>
      ) : null}
      <Dialog
        open={qrRoute !== null}
        onOpenChange={(open) => {
          if (!open) setQrRoute(null);
        }}
      >
        <DialogContent onOpenAutoFocus={(e) => e.preventDefault()}>
          <DialogHeader>
            <DialogTitle>QR-код VLESS: {clientName}</DialogTitle>
            <p className="text-sm text-muted-foreground">
              {qrRoute ? vlessRouteLabel(qrRoute) : ""}: отсканируйте QR в v2RayTun, Happ
              или другом приложении с поддержкой VLESS
            </p>
          </DialogHeader>
          {qrRoute ? (
            <img
              className="mx-auto aspect-square w-full"
              alt={`QR-код VLESS клиента ${clientName}, маршрут «${vlessRouteLabel(qrRoute)}»`}
              src={vlessQrUrl(clientId, qrRoute.entry)}
            />
          ) : null}
        </DialogContent>
      </Dialog>
    </div>
  );
}

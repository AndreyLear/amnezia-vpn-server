import { useEffect, useState } from "react";
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

// Раздел VLESS в окне клиента (amnezia-vpn-server-14y0.5): второй способ
// подключения рядом с AmneziaWG. На мобильном интернете в РФ AmneziaWG
// режется, а VLESS+XHTTP+Reality на 443 проходит — напрямую или через
// российский узел-вход. Тексты согласованы с редактором.

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
};

type State =
  | { kind: "loading" }
  | { kind: "error" }
  | { kind: "ready"; info: VlessInfo };

export function VlessSection({ clientId, clientName, enabled, load }: VlessSectionProps) {
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

  if (state.kind === "loading") return null;
  if (state.kind === "error") {
    return (
      <div className="grid gap-0.5 py-2" data-testid="vless-section">
        <dt className="text-muted-foreground">VLESS</dt>
        <dd className="text-sm text-destructive">
          Не удалось загрузить ссылки VLESS, откройте окно клиента заново
        </dd>
      </div>
    );
  }
  const { info } = state;
  if (!info.enabled) {
    return (
      <div className="grid gap-0.5 py-2" data-testid="vless-section">
        <dt className="text-muted-foreground">VLESS</dt>
        <dd className="text-sm text-muted-foreground">
          Клиент на паузе — ссылки VLESS появятся после включения
        </dd>
      </div>
    );
  }
  // Xray на сервере не настроен и узлов нет — раздела нет совсем.
  if (info.routes.length === 0) return null;

  return (
    <div className="grid gap-1 py-2" data-testid="vless-section">
      <dt className="text-muted-foreground">VLESS</dt>
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

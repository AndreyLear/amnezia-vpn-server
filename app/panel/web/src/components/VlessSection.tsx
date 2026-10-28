import { useEffect, useState, type ReactNode } from "react";
import { Copy, QrCode } from "lucide-react";
import { toast } from "sonner";

import { InfoSection, infoRowClass, infoRowsClass } from "@/components/InfoBlock";
import { SpeedChart } from "@/components/SpeedChart";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { vlessQrUrl, type VlessInfo, type VlessRoute } from "@/lib/api";
import { formatBytes, formatHandshake } from "@/lib/format";
import { cn } from "@/lib/utils";

// Раздел VLESS в окне клиента (amnezia-vpn-server-14y0.5): второй способ
// подключения рядом с AmneziaWG. На мобильном интернете в РФ AmneziaWG
// режется, а VLESS+XHTTP+Reality на 443 проходит — напрямую или через
// российский узел-вход. Тексты согласованы с редактором.
//
// С amnezia-vpn-server-14y0.8 раздел — содержимое вкладки «VLESS»: кроме
// ссылок в нём трафик VLESS и последняя активность — по счётчикам Xray.
// Вход пересылает каждого клиента под его UUID, так что трафик через вход
// учтён тоже, и оговорки про него больше нет (amnezia-vpn-server-14y0.12).
//
// С amnezia-vpn-server-14y0.9 раздел — несколько разделов карточки
// вкладок (не своих карточек): график скорости VLESS (свой журнал панели по
// тем же счётчикам), трафик с активностью и ссылки.

export function vlessRouteLabel(route: VlessRoute): string {
  if (!route.entry) return "Напрямую";
  return `Через вход ${route.prefix.trim() || route.entry}`;
}

export function vlessRouteHint(route: VlessRoute): string {
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
    <div className={cn("grid gap-0.5", infoRowClass)}>
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

  if (state.kind === "loading") return null;
  if (state.kind === "error") {
    return (
      <InfoSection data-testid="vless-section">
        <p className="text-destructive">
          Не удалось загрузить ссылки VLESS, откройте окно клиента заново
        </p>
      </InfoSection>
    );
  }
  const { info } = state;
  // Xray на сервере не настроен и узлов нет — ни ссылок, ни трафика.
  if (info.enabled && info.routes.length === 0) {
    return (
      <InfoSection data-testid="vless-section">
        <p className="text-muted-foreground">VLESS на сервере не настроен</p>
      </InfoSection>
    );
  }
  // Разделы — как у вкладки AWG (amnezia-vpn-server-14y0.9): график первым,
  // рядом с ним то, что он показывает в цифрах, ссылки — отдельно.
  const chart = (
    <InfoSection>
      <dl>
        <SpeedChart clientId={clientId} protocol="vless" />
      </dl>
    </InfoSection>
  );
  const usage = (
    <InfoSection>
      <dl className={infoRowsClass}>
        <Field label="Трафик">
          ↓ {formatBytes(traffic.down)} · ↑ {formatBytes(traffic.up)}
        </Field>
        <Field label="Последняя активность">{formatHandshake(traffic.lastActive)}</Field>
      </dl>
    </InfoSection>
  );
  if (!info.enabled) {
    return (
      <>
        {chart}
        {usage}
        <InfoSection data-testid="vless-section">
          <p className="text-muted-foreground">
            Клиент на паузе — ссылки VLESS появятся после включения
          </p>
        </InfoSection>
      </>
    );
  }

  return (
    <>
      {chart}
      {usage}
      <InfoSection data-testid="vless-section">
        <ul aria-label="Маршруты" className={infoRowsClass}>
          {info.routes.map((route) => {
            const label = vlessRouteLabel(route);
            return (
              <li
                key={route.entry || "direct"}
                // Кнопки — под подписью на любой ширине: рядом с ними
                // подсказка входа ломалась в три строки, а окно владелец
                // просил не расширять (amnezia-vpn-server-14y0.13).
                className={cn("flex flex-col gap-2", infoRowClass)}
              >
                <div className="min-w-0 flex-1">
                  <div>{label}</div>
                  <div className="text-muted-foreground">{vlessRouteHint(route)}</div>
                </div>
                <div className="flex shrink-0 gap-2">
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
                </div>
              </li>
            );
          })}
        </ul>
      </InfoSection>
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
    </>
  );
}

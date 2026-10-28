import { useEffect, useState, type ReactNode } from "react";

import { Segment } from "@/components/Segment";
import { vlessRouteHint, vlessRouteLabel } from "@/components/VlessSection";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { vlessQrUrl, type VlessInfo } from "@/lib/api";
import { protocolOptions, type Protocol } from "@/lib/protocol";

type QrDialogProps = {
  clientId: number | null;
  clientName: string;
  onOpenChange: (open: boolean) => void;
  // С какого протокола открыть: из меню строки — по последней активности
  // клиента, из вкладки AWG окна клиента — AWG (amnezia-vpn-server-14y0.13).
  initialProtocol?: Protocol;
  // Ссылки VLESS клиента; без загрузчика окно — только AWG, как раньше.
  loadVless?: (id: number) => Promise<VlessInfo>;
};

type VlessState =
  | { kind: "loading" }
  | { kind: "error" }
  | { kind: "ready"; info: VlessInfo };

// Маршрут «напрямую» — пустой entry; в сегменте нужен непустой ключ.
const directKey = "@direct";

/**
 * Окно «QR-код» (amnezia-vpn-server-14y0.13). Пункт меню строки клиента
 * показывал только AmneziaWG, а часть клиентов ходит только по VLESS.
 * Теперь в окне выбор протокола — тем же сегментом первого уровня, что в
 * окне клиента, — а у VLESS ещё выбор маршрута сегментом второго уровня,
 * если у сервера есть входы. Тексты согласованы с редактором.
 */
export function QrDialog({
  clientId,
  clientName,
  onOpenChange,
  initialProtocol = "awg",
  loadVless,
}: QrDialogProps) {
  const [protocol, setProtocol] = useState<Protocol>(initialProtocol);
  const [vless, setVless] = useState<VlessState>({ kind: "loading" });
  const [routeKey, setRouteKey] = useState(directKey);

  // Протокол и маршрут выбираются заново при каждом открытии: окно
  // открывают для конкретного клиента, и прошлый выбор к нему не относится.
  useEffect(() => {
    if (clientId === null) return;
    setProtocol(loadVless ? initialProtocol : "awg");
    setRouteKey(directKey);
    setVless({ kind: "loading" });
  }, [clientId, initialProtocol, loadVless]);

  const wantVless = clientId !== null && protocol === "vless" && loadVless !== undefined;
  useEffect(() => {
    if (!wantVless || clientId === null || !loadVless) return;
    let cancelled = false;
    loadVless(clientId).then(
      (info) => {
        if (!cancelled) setVless({ kind: "ready", info });
      },
      () => {
        if (!cancelled) setVless({ kind: "error" });
      },
    );
    return () => {
      cancelled = true;
    };
  }, [wantVless, clientId, loadVless]);

  let hint: string | null = null;
  let body: ReactNode = null;
  if (clientId !== null && protocol === "awg") {
    // Названия приложений здесь — примеры, а не список: конфигурацию
    // читает любой клиент, понимающий протокол. Ссылок нет намеренно —
    // кому нужно, тот найдёт приложение по названию, а три адреса на две
    // строки в маленьком окне мешают больше, чем помогают
    // (amnezia-vpn-server-d86w).
    //
    // Уровень 2.0 назван не наугад: набор параметров, который мы выдаём
    // (Jc/Jmin/Jmax, S1-S4, H1-H4, I1-I5), — это именно он
    // (internal/awgconf/generator.go). Клиент постарше конфиг не прочтёт,
    // поэтому цифра в подписи важнее вежливости.
    hint =
      "Отсканируйте QR в AmneziaWG, WG Tunnel или другом приложении с поддержкой AmneziaWG 2.0 и выше";
    // The symbol fills the dialog instead of sitting in a fixed 256 px box:
    // a client config needs 89 modules, and a camera reading them off a
    // screen needs every pixel of pitch it can get (T-ky6l). The PNG is
    // rendered far larger than shown, so the browser only ever scales it
    // down.
    body = (
      <img
        className="mx-auto aspect-square w-full"
        alt={`QR-код AmneziaWG клиента ${clientName}`}
        src={`/clients/${clientId}/qr`}
      />
    );
  } else if (clientId !== null && vless.kind === "error") {
    body = (
      <p className="text-sm text-destructive">Не удалось загрузить QR VLESS, откройте окно заново</p>
    );
  } else if (clientId !== null && vless.kind === "ready") {
    const { info } = vless;
    if (!info.enabled) {
      body = (
        <p className="text-sm text-muted-foreground">
          Клиент на паузе — QR VLESS появится после включения
        </p>
      );
    } else if (info.routes.length === 0) {
      body = <p className="text-sm text-muted-foreground">VLESS на сервере не настроен</p>;
    } else {
      hint = "Отсканируйте QR в v2RayTun, Happ или другом приложении с поддержкой VLESS";
      const route =
        info.routes.find((r) => (r.entry || directKey) === routeKey) ?? info.routes[0];
      const label = vlessRouteLabel(route);
      body = (
        <>
          {/* Маршрут выбирают только когда есть из чего: без входов он
              один. Подсказка — та же, что у маршрута в окне клиента: без
              неё выбор между «напрямую» и «через вход» слепой. */}
          {info.routes.length > 1 ? (
            <div className="grid gap-2">
              <Segment
                label="Маршрут"
                size="sm"
                value={route.entry || directKey}
                onValueChange={setRouteKey}
                options={info.routes.map((r) => ({
                  value: r.entry || directKey,
                  label: vlessRouteLabel(r),
                }))}
              />
              <p className="text-sm text-muted-foreground">{vlessRouteHint(route)}</p>
            </div>
          ) : null}
          <img
            className="mx-auto aspect-square w-full"
            alt={`QR-код VLESS клиента ${clientName}, маршрут «${label}»`}
            src={vlessQrUrl(clientId, route.entry)}
          />
        </>
      );
    }
  }

  return (
    <Dialog open={clientId !== null} onOpenChange={onOpenChange}>
      <DialogContent onOpenAutoFocus={(e) => e.preventDefault()}>
        <DialogHeader>
          <DialogTitle>QR-код: {clientName}</DialogTitle>
          {hint ? <p className="text-sm text-muted-foreground">{hint}</p> : null}
        </DialogHeader>
        {loadVless ? (
          <Segment
            label="Протокол"
            value={protocol}
            onValueChange={setProtocol}
            options={protocolOptions}
          />
        ) : null}
        {body}
      </DialogContent>
    </Dialog>
  );
}

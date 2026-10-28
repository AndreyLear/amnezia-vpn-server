import { useEffect, useState, type ReactNode } from "react";

import { Segment } from "@/components/Segment";
import { vlessRouteLabel } from "@/components/VlessSection";
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
 *
 * С amnezia-vpn-server-14y0.17 окно без пояснений (владелец счёл его
 * перегруженным): ни подзаголовка с приложениями, ни строки под маршрутом.
 * Маршрут — справа в строке вкладок, а не отдельной строкой под ними.
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

  // Справа от вкладок — выбор маршрута VLESS, если есть из чего выбирать.
  let route: ReactNode = null;
  let body: ReactNode = null;
  if (clientId !== null && protocol === "awg") {
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
      const current =
        info.routes.find((r) => (r.entry || directKey) === routeKey) ?? info.routes[0];
      const label = vlessRouteLabel(current, info.routes);
      // Маршрут выбирают только когда есть из чего: без входов он один.
      // ml-auto держит его справа и тогда, когда на узком экране он
      // переносится под вкладки (amnezia-vpn-server-14y0.17).
      if (info.routes.length > 1) {
        route = (
          <Segment
            label="Маршрут"
            size="sm"
            className="ml-auto"
            value={current.entry || directKey}
            onValueChange={setRouteKey}
            options={info.routes.map((r) => ({
              value: r.entry || directKey,
              label: vlessRouteLabel(r, info.routes),
            }))}
          />
        );
      }
      body = (
        <img
          className="mx-auto aspect-square w-full"
          alt={`QR-код VLESS клиента ${clientName}, маршрут «${label}»`}
          src={vlessQrUrl(clientId, current.entry)}
        />
      );
    }
  }

  return (
    <Dialog open={clientId !== null} onOpenChange={onOpenChange}>
      {/* Шапка → содержимое 24px, строка вкладок → QR 16px — правила UI
          владельца, как в окне клиента (amnezia-vpn-server-14y0.17). */}
      <DialogContent bodyClassName="gap-6" onOpenAutoFocus={(e) => e.preventDefault()}>
        <DialogHeader>
          <DialogTitle>QR-код: {clientName}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-4">
          {loadVless ? (
            <div className="flex flex-wrap items-center justify-between gap-4">
              <Segment
                label="Протокол"
                value={protocol}
                onValueChange={setProtocol}
                options={protocolOptions}
              />
              {route}
            </div>
          ) : null}
          {body}
        </div>
      </DialogContent>
    </Dialog>
  );
}

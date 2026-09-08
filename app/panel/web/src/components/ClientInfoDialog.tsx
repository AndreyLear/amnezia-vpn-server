import { useEffect, useState, type ReactNode } from "react";
import { Download, Pause, Pencil, Play, QrCode, Trash2 } from "lucide-react";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { UserText } from "@/components/UserText";
import type { Client } from "@/lib/api";
import { formatBytes, formatHandshake } from "@/lib/format";
import { cn } from "@/lib/utils";

// Границы MTU повторяют серверные (db.ClientMTUFloor/Ceiling): ниже нижней
// туннель не может нести IPv6, выше верхней полный пакет не влезает на
// провод с обычными 1500 байтами. Сервер всё равно проверит своё, но человек
// не должен упираться в отказ, уже нажав «Сохранить» (amnezia-vpn-server-h2pg).
const mtuFloor = 1280;
const mtuCeiling = 1440;

// Границы предела скорости повторяют серверные (db.ClientRateFloor/Ceiling):
// ниже мегабита ограничение перестаёт быть ограничением и становится обрывом
// связи, выше гигабита его не выдержит ни одно плечо, ради которого оно
// заводилось (amnezia-vpn-server-jzzu).
const rateFloor = 1;
const rateCeiling = 1000;

const confirmButtonClass = "max-sm:h-12 max-sm:w-full";
const saveButtonClass = "max-sm:h-12 max-sm:w-full";

type ClientInfoDialogProps = {
  client: Client | null;
  pending?: boolean;
  onOpenChange: (open: boolean) => void;
  onSave?: (
    payload: { name: string; description: string; mtu?: number; rate_limit?: number },
  ) => boolean | void | Promise<boolean | void>;
  onQr?: () => void;
  onDownload?: () => void;
  onToggle?: () => void;
  onDelete?: () => void;
};

function PropertyRow({
  actions,
  className,
  children,
}: {
  actions?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn("flex items-center gap-2 py-2", className)}>
      <div className="min-w-0 flex-1">{children}</div>
      {actions}
    </div>
  );
}

function ReadOnlyProperty({
  label,
  optional,
  actions,
  children,
}: {
  label: string;
  optional?: boolean;
  actions: ReactNode;
  children: ReactNode;
}) {
  const caption = optional ? (
    <>
      {label}{" "}
      <span className="font-normal text-muted-foreground">(опционально)</span>
    </>
  ) : (
    label
  );

  return (
    <PropertyRow actions={actions}>
      <dt className="text-muted-foreground">{caption}</dt>
      {children}
    </PropertyRow>
  );
}

export function ClientInfoDialog({
  client,
  pending,
  onOpenChange,
  onSave,
  onQr,
  onDownload,
  onToggle,
  onDelete,
}: ClientInfoDialogProps) {
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [viewName, setViewName] = useState("");
  const [viewDescription, setViewDescription] = useState("");
  const [viewMTU, setViewMTU] = useState(0);
  const [editingMTU, setEditingMTU] = useState(false);
  const [mtuDraft, setMTUDraft] = useState("");
  const [viewRate, setViewRate] = useState(0);
  const [editingRate, setEditingRate] = useState(false);
  const [rateDraft, setRateDraft] = useState("");
  const [nameDraft, setNameDraft] = useState("");
  const [descriptionDraft, setDescriptionDraft] = useState("");
  const [editingName, setEditingName] = useState(false);
  const [editingDescription, setEditingDescription] = useState(false);

  useEffect(() => {
    setViewName(client?.name ?? "");
    setViewMTU(client?.mtu ?? 0);
    setViewRate(client?.rate_limit ?? 0);
    setViewDescription(client?.description ?? "");
    setNameDraft(client?.name ?? "");
    setDescriptionDraft(client?.description ?? "");
    setEditingName(false);
    setEditingDescription(false);
  }, [client?.id, client == null]);

  useEffect(() => {
    setViewName(client?.name ?? "");
  }, [client?.name]);

  useEffect(() => {
    setViewDescription(client?.description ?? "");
  }, [client?.description]);

  const committedDescription = client?.description ?? "";

  function startNameEdit() {
    setNameDraft(viewName);
    setEditingName(true);
  }

  function startDescriptionEdit() {
    setDescriptionDraft(viewDescription);
    setEditingDescription(true);
  }

  function cancelNameEdit() {
    setNameDraft(viewName);
    setEditingName(false);
  }

  function cancelDescriptionEdit() {
    setDescriptionDraft(viewDescription);
    setEditingDescription(false);
  }

  async function saveName() {
    if (!client) return;
    if (!nameDraft.trim()) return;
    if (nameDraft === client.name) {
      setEditingName(false);
      return;
    }
    const payload = { name: nameDraft, description: committedDescription };
    const saved = await onSave?.(payload);
    if (saved) {
      setViewName(payload.name);
      setEditingName(false);
    }
  }

  function startMTUEdit() {
    setMTUDraft(viewMTU === 0 ? "" : String(viewMTU));
    setEditingMTU(true);
  }

  function cancelMTUEdit() {
    setMTUDraft(viewMTU === 0 ? "" : String(viewMTU));
    setEditingMTU(false);
  }

  // Пустое поле — снятие своего значения, поэтому оно допустимо.
  const rateDraftValue = rateDraft.trim();
  const rateOutOfRange =
    rateDraftValue !== "" &&
    (!/^\d+$/.test(rateDraftValue) ||
      Number(rateDraftValue) < rateFloor ||
      Number(rateDraftValue) > rateCeiling);

  const mtuDraftValue = mtuDraft.trim();
  const mtuOutOfRange =
    mtuDraftValue !== "" &&
    (!/^\d+$/.test(mtuDraftValue) ||
      Number(mtuDraftValue) < mtuFloor ||
      Number(mtuDraftValue) > mtuCeiling);

  async function saveMTU() {
    if (!client) return;
    // Пустое поле — «как у сервера»: снять своё значение и оставить его
    // нельзя одним и тем же действием, поэтому пустота и есть снятие.
    if (mtuOutOfRange) return;
    const next = mtuDraftValue === "" ? 0 : Number(mtuDraftValue);
    if (next === viewMTU) {
      setEditingMTU(false);
      return;
    }
    const saved = await onSave?.({
      name: client.name,
      description: committedDescription,
      mtu: next,
    });
    if (saved) {
      setViewMTU(next);
      setEditingMTU(false);
    }
  }

  function startRateEdit() {
    setRateDraft(viewRate === 0 ? "" : String(viewRate));
    setEditingRate(true);
  }

  function cancelRateEdit() {
    setRateDraft(viewRate === 0 ? "" : String(viewRate));
    setEditingRate(false);
  }

  async function saveRate() {
    if (!client) return;
    // Пустое поле — «без предела»: снять и задать одним действием нельзя,
    // поэтому пустота и есть снятие.
    if (rateOutOfRange) return;
    const next = rateDraftValue === "" ? 0 : Number(rateDraftValue);
    if (next === viewRate) {
      setEditingRate(false);
      return;
    }
    const saved = await onSave?.({
      name: client.name,
      description: committedDescription,
      rate_limit: next,
    });
    if (saved) {
      setViewRate(next);
      setEditingRate(false);
    }
  }

  async function saveDescription() {
    if (!client) return;
    if (descriptionDraft === committedDescription) {
      setEditingDescription(false);
      return;
    }
    const payload = { name: client.name, description: descriptionDraft };
    const saved = await onSave?.(payload);
    if (saved) {
      setViewDescription(payload.description);
      setEditingDescription(false);
    }
  }

  return (
    <>
      <Dialog open={client !== null} onOpenChange={onOpenChange}>
        <DialogContent
          className="sm:max-w-md pb-6"
          onOpenAutoFocus={(e) => e.preventDefault()}
        >
          {client ? (
            <>
              <DialogHeader>
                <DialogTitle>Клиент</DialogTitle>
              </DialogHeader>
              <div className="grid gap-4">
                <dl className="grid divide-y divide-border gap-0 text-sm">
                  <ReadOnlyProperty
                    label="Имя"
                    actions={
                      <Button
                        type="button"
                        variant="outline"
                        aria-label="Изменить имя"
                        disabled={pending}
                        onClick={startNameEdit}
                      >
                        <Pencil data-icon="inline-start" aria-hidden />
                        Изменить
                      </Button>
                    }
                  >
                    <dd>
                      <UserText>{viewName}</UserText>
                    </dd>
                  </ReadOnlyProperty>
                  <ReadOnlyProperty
                    label="Описание"
                    optional
                    actions={
                      <Button
                        type="button"
                        variant="outline"
                        aria-label="Изменить описание"
                        disabled={pending}
                        onClick={startDescriptionEdit}
                      >
                        <Pencil data-icon="inline-start" aria-hidden />
                        Изменить
                      </Button>
                    }
                  >
                    <dd>
                      <UserText>{viewDescription}</UserText>
                    </dd>
                  </ReadOnlyProperty>
                  <ReadOnlyProperty
                    label="MTU"
                    actions={
                      <Button
                        type="button"
                        variant="outline"
                        aria-label="Изменить MTU"
                        disabled={pending}
                        onClick={startMTUEdit}
                      >
                        <Pencil data-icon="inline-start" aria-hidden />
                        Изменить
                      </Button>
                    }
                  >
                    <dd>
                      {viewMTU === 0 ? "как у сервера" : viewMTU}
                    </dd>
                  </ReadOnlyProperty>
                  <ReadOnlyProperty
                    label="Предел скорости"
                    actions={
                      <Button
                        type="button"
                        variant="outline"
                        aria-label="Изменить предел скорости"
                        disabled={pending}
                        onClick={startRateEdit}
                      >
                        <Pencil data-icon="inline-start" aria-hidden />
                        Изменить
                      </Button>
                    }
                  >
                    <dd>{viewRate === 0 ? "без предела" : `${viewRate} Мбит/с`}</dd>
                  </ReadOnlyProperty>
                  <PropertyRow
                    actions={
                      <Button
                        type="button"
                        variant="outline"
                        disabled={pending}
                        onClick={onToggle}
                      >
                        {client.enabled ? (
                          <>
                            <Pause data-icon="inline-start" aria-hidden />
                            Отключить
                          </>
                        ) : (
                          <>
                            <Play data-icon="inline-start" aria-hidden />
                            Включить
                          </>
                        )}
                      </Button>
                    }
                  >
                    <div className="grid gap-0.5">
                      <dt className="text-muted-foreground">Статус</dt>
                      <dd>
                        {!client.enabled
                          ? "пауза"
                          : client.online
                            ? "онлайн"
                            : "офлайн"}
                      </dd>
                    </div>
                  </PropertyRow>
                  <PropertyRow
                    actions={
                      <>
                        <Button
                          type="button"
                          variant="outline"
                          disabled={pending}
                          onClick={onDownload}
                        >
                          <Download data-icon="inline-start" aria-hidden />
                          Конфиг
                        </Button>
                        <Button
                          type="button"
                          variant="outline"
                          disabled={pending}
                          onClick={onQr}
                        >
                          <QrCode data-icon="inline-start" aria-hidden />
                          QR
                        </Button>
                      </>
                    }
                  >
                    <div className="grid gap-0.5">
                      <dt className="text-muted-foreground">IP</dt>
                      <dd className="font-mono">{client.address}</dd>
                      {/* Выданный конфиг несёт оба адреса, значит и панель
                          должна показывать оба: иначе владелец не видит того,
                          что роздал (amnezia-vpn-server-lhlv). Пусто, когда
                          туннель несёт только IPv4. */}
                      {client.address6 ? (
                        <dd className="font-mono">{client.address6}</dd>
                      ) : null}
                    </div>
                  </PropertyRow>
                  <PropertyRow>
                    <div className="grid gap-0.5">
                      <dt className="text-muted-foreground">Handshake</dt>
                      <dd>{formatHandshake(client.last_handshake_utc)}</dd>
                    </div>
                  </PropertyRow>
                  <PropertyRow>
                    <div className="grid gap-0.5">
                      <dt className="text-muted-foreground">Трафик</dt>
                      <dd>
                        ↓ {formatBytes(client.tx_bytes)} · ↑ {formatBytes(client.rx_bytes)}
                      </dd>
                    </div>
                  </PropertyRow>
                  <PropertyRow className="pt-3">
                    <Button
                      type="button"
                      variant="destructive"
                      disabled={pending}
                      onClick={() => setConfirmOpen(true)}
                    >
                      <Trash2 data-icon="inline-start" />
                      Удалить
                    </Button>
                  </PropertyRow>
                </dl>
              </div>
            </>
          ) : null}
        </DialogContent>
      </Dialog>
      <Dialog
        open={editingName}
        onOpenChange={(open) => {
          if (!open) cancelNameEdit();
        }}
      >
        <DialogContent className="gap-6 sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Имя</DialogTitle>
          </DialogHeader>
          <form
            className="grid gap-6"
            onSubmit={(e) => {
              e.preventDefault();
              void saveName();
            }}
          >
            <div className="grid gap-2">
              <Label htmlFor="info-name">Имя</Label>
              <Input
                id="info-name"
                required
                maxLength={64}
                value={nameDraft}
                onChange={(e) => setNameDraft(e.target.value)}
                disabled={pending}
              />
            </div>
            <DialogFooter>
              <Button
                type="submit"
                className={saveButtonClass}
                aria-label="Сохранить имя"
                disabled={pending}
              >
                Сохранить
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog
        open={editingMTU}
        onOpenChange={(open) => {
          if (!open) cancelMTUEdit();
        }}
      >
        <DialogContent className="gap-6 sm:max-w-md">
          <DialogHeader>
            <DialogTitle>MTU</DialogTitle>
          </DialogHeader>
          <form
            className="grid gap-6"
            onSubmit={(e) => {
              e.preventDefault();
              void saveMTU();
            }}
          >
            <div className="grid gap-2">
              <Label htmlFor="info-mtu">Размер пакета, байт</Label>
              <Input
                id="info-mtu"
                type="number"
                min={mtuFloor}
                max={mtuCeiling}
                inputMode="numeric"
                placeholder="как у сервера"
                value={mtuDraft}
                onChange={(e) => setMTUDraft(e.target.value)}
                disabled={pending}
              />
              <p className="text-sm text-muted-foreground">
                Пусто — как у сервера. Своё значение задаётся в пределах от{" "}
                {mtuFloor} до {mtuCeiling}: больше не помещается на обычном
                канале в 1500 байт, потому что сам туннель занимает 60. Поднять
                имеет смысл там, где канал заведомо хороший, — например роутер
                на проводе.
              </p>
              <p className="text-sm text-muted-foreground">
                Значение попадёт в настройки, которые вы выдадите после этого.
                Тем, кто уже подключён, выдавать новые не обязательно — то же
                число можно вписать вручную в приложении, в настройках
                подключения
              </p>
            </div>
            <DialogFooter>
              <Button
                type="submit"
                className={saveButtonClass}
                aria-label="Сохранить MTU"
                disabled={pending || mtuOutOfRange}
              >
                Сохранить
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog
        open={editingRate}
        onOpenChange={(open) => {
          if (!open) cancelRateEdit();
        }}
      >
        <DialogContent className="gap-6 sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Предел скорости</DialogTitle>
          </DialogHeader>
          <form
            className="grid gap-6"
            onSubmit={(e) => {
              e.preventDefault();
              void saveRate();
            }}
          >
            <div className="grid gap-2">
              <Label htmlFor="info-rate">Мегабит в секунду</Label>
              <Input
                id="info-rate"
                type="number"
                min={rateFloor}
                max={rateCeiling}
                inputMode="numeric"
                placeholder="без предела"
                value={rateDraft}
                onChange={(e) => setRateDraft(e.target.value)}
                disabled={pending}
              />
              {/* Подпись говорит, для чего это и что даёт. Прежняя обещала
                  прежнюю скорость, которой на замере нет
                  (amnezia-vpn-server-ouhb). */}
              <p className="text-sm text-muted-foreground">
                Держит скорость ровной: пропадают рывки и повторные передачи.
                Видео не встаёт, звонки не рассыпаются
              </p>
              <p className="text-sm text-muted-foreground">
                Оставьте поле пустым, чтобы убрать ограничение
              </p>
            </div>
            <DialogFooter>
              <Button
                type="submit"
                className={saveButtonClass}
                aria-label="Сохранить предел скорости"
                disabled={pending || rateOutOfRange}
              >
                Сохранить
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog
        open={editingDescription}
        onOpenChange={(open) => {
          if (!open) cancelDescriptionEdit();
        }}
      >
        <DialogContent className="gap-6 sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Описание</DialogTitle>
          </DialogHeader>
          <form
            className="grid gap-6"
            onSubmit={(e) => {
              e.preventDefault();
              void saveDescription();
            }}
          >
            <div className="grid gap-2">
              <Label htmlFor="info-description">
                Описание{" "}
                <span className="font-normal text-muted-foreground">
                  (опционально)
                </span>
              </Label>
              <Textarea
                id="info-description"
                className="field-sizing-content min-h-8 resize-none"
                rows={1}
                value={descriptionDraft}
                onChange={(e) => setDescriptionDraft(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.stopPropagation();
                  }
                }}
                disabled={pending}
              />
            </div>
            <DialogFooter>
              <Button
                type="submit"
                className={saveButtonClass}
                aria-label="Сохранить описание"
                disabled={pending}
              >
                Сохранить
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Удалить клиента «<UserText>{client?.name}</UserText>»?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Конфигурация клиента будет убрана из awg0.conf
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel className={confirmButtonClass}>
              Отмена
            </AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              className={confirmButtonClass}
              disabled={pending}
              onClick={onDelete}
            >
              Удалить
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

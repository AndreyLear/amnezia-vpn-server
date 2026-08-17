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
import type { Client } from "@/lib/api";
import { formatBytes, formatHandshake } from "@/lib/format";
import { cn } from "@/lib/utils";

const confirmButtonClass = "max-sm:h-12 max-sm:w-full";
const saveButtonClass = "max-sm:h-12 max-sm:w-full";

type ClientInfoDialogProps = {
  client: Client | null;
  pending?: boolean;
  onOpenChange: (open: boolean) => void;
  onSave?: (
    payload: { name: string; description: string },
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
  const [nameDraft, setNameDraft] = useState("");
  const [descriptionDraft, setDescriptionDraft] = useState("");
  const [editingName, setEditingName] = useState(false);
  const [editingDescription, setEditingDescription] = useState(false);

  useEffect(() => {
    setViewName(client?.name ?? "");
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
                    <dd>{viewName}</dd>
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
                    <dd>{viewDescription}</dd>
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
                        ↓ {formatBytes(client.rx_bytes)} · ↑ {formatBytes(client.tx_bytes)}
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
            <AlertDialogTitle>Удалить клиента «{client?.name}»?</AlertDialogTitle>
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

import { useEffect, useState, type ReactNode } from "react";
import { Download, Power, QrCode, Trash2 } from "lucide-react";

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
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import type { Client } from "@/lib/api";
import { formatBytes, formatHandshake } from "@/lib/format";

const actionButtonClass = "max-sm:h-12 max-sm:w-full";

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

function PropertyEditButtons({
  editing,
  pending,
  editLabel,
  saveLabel,
  cancelLabel,
  onEdit,
  onSave,
  onCancel,
}: {
  editing: boolean;
  pending?: boolean;
  editLabel: string;
  saveLabel: string;
  cancelLabel: string;
  onEdit: () => void;
  onSave: () => void;
  onCancel: () => void;
}) {
  if (editing) {
    return (
      <div className="flex">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="px-1"
          aria-label={saveLabel}
          disabled={pending}
          onClick={onSave}
        >
          Сохранить
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="px-1"
          aria-label={cancelLabel}
          disabled={pending}
          onClick={onCancel}
        >
          Отменить
        </Button>
      </div>
    );
  }

  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      className="px-1"
      aria-label={editLabel}
      disabled={pending}
      onClick={onEdit}
    >
      Изменить
    </Button>
  );
}

function EditableProperty({
  editing,
  htmlFor,
  label,
  optional,
  actions,
  children,
}: {
  editing: boolean;
  htmlFor: string;
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
    <div className="flex items-center gap-2 max-sm:py-2">
      <div className="min-w-0 flex-1">
        {editing ? (
          <Label htmlFor={htmlFor} className="text-muted-foreground">
            {caption}
          </Label>
        ) : (
          <dt className="text-muted-foreground">{caption}</dt>
        )}
        {children}
      </div>
      {actions}
    </div>
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
          className="h-auto max-h-[calc(100dvh-2rem)] overflow-y-auto max-sm:top-auto max-sm:right-0 max-sm:bottom-0 max-sm:left-0 max-sm:w-full max-sm:max-w-none max-sm:translate-x-0 max-sm:translate-y-0 max-sm:rounded-b-none sm:max-w-md"
          onOpenAutoFocus={(e) => e.preventDefault()}
        >
          {client ? (
            <>
              <DialogHeader>
                <DialogTitle>Клиент</DialogTitle>
              </DialogHeader>
              <div className="grid gap-4">
                <dl className="grid gap-2 text-sm max-sm:divide-y max-sm:divide-border max-sm:gap-0">
                  <EditableProperty
                    editing={editingName}
                    htmlFor="info-name"
                    label="Имя"
                    actions={
                      <PropertyEditButtons
                        editing={editingName}
                        pending={pending}
                        editLabel="Изменить имя"
                        saveLabel="Сохранить имя"
                        cancelLabel="Отменить имя"
                        onEdit={startNameEdit}
                        onSave={() => void saveName()}
                        onCancel={cancelNameEdit}
                      />
                    }
                  >
                    {editingName ? (
                      <Input
                        id="info-name"
                        required
                        maxLength={64}
                        value={nameDraft}
                        onChange={(e) => setNameDraft(e.target.value)}
                        disabled={pending}
                      />
                    ) : (
                      <dd>{viewName}</dd>
                    )}
                  </EditableProperty>
                  <EditableProperty
                    editing={editingDescription}
                    htmlFor="info-description"
                    label="Описание"
                    optional
                    actions={
                      <PropertyEditButtons
                        editing={editingDescription}
                        pending={pending}
                        editLabel="Изменить описание"
                        saveLabel="Сохранить описание"
                        cancelLabel="Отменить описание"
                        onEdit={startDescriptionEdit}
                        onSave={() => void saveDescription()}
                        onCancel={cancelDescriptionEdit}
                      />
                    }
                  >
                    {editingDescription ? (
                      <Textarea
                        id="info-description"
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
                    ) : (
                      <dd>{viewDescription}</dd>
                    )}
                  </EditableProperty>
                  <div className="grid gap-0.5 max-sm:py-2">
                    <dt className="text-muted-foreground">Статус</dt>
                    <dd>
                      {!client.enabled
                        ? "пауза"
                        : client.online
                          ? "онлайн"
                          : "офлайн"}
                    </dd>
                  </div>
                  <div className="grid gap-0.5 max-sm:py-2">
                    <dt className="text-muted-foreground">IP</dt>
                    <dd className="font-mono">{client.address}</dd>
                  </div>
                  <div className="grid gap-0.5 max-sm:py-2">
                    <dt className="text-muted-foreground">Handshake</dt>
                    <dd>{formatHandshake(client.last_handshake_utc)}</dd>
                  </div>
                  <div className="grid gap-0.5 max-sm:py-2">
                    <dt className="text-muted-foreground">Трафик</dt>
                    <dd>
                      ↓ {formatBytes(client.rx_bytes)} · ↑ {formatBytes(client.tx_bytes)}
                    </dd>
                  </div>
                </dl>
                <div className="flex flex-wrap gap-2 max-sm:flex-col">
                  <Button
                    type="button"
                    variant="outline"
                    className={actionButtonClass}
                    disabled={pending}
                    onClick={onQr}
                  >
                    <QrCode data-icon="inline-start" />
                    QR-код
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    className={actionButtonClass}
                    disabled={pending}
                    onClick={onDownload}
                  >
                    <Download data-icon="inline-start" />
                    Скачать конфиг
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    className={actionButtonClass}
                    disabled={pending}
                    onClick={onToggle}
                  >
                    <Power data-icon="inline-start" />
                    {client.enabled ? "Отключить" : "Включить"}
                  </Button>
                  <Button
                    type="button"
                    variant="destructive"
                    className={actionButtonClass}
                    disabled={pending}
                    onClick={() => setConfirmOpen(true)}
                  >
                    <Trash2 data-icon="inline-start" />
                    Удалить
                  </Button>
                </div>
              </div>
            </>
          ) : null}
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
            <AlertDialogCancel>Отмена</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
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

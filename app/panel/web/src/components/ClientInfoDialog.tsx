import { useEffect, useState } from "react";

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
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");

  useEffect(() => {
    setName(client?.name ?? "");
    setDescription(client?.description ?? "");
  }, [client?.id, client == null]);

  return (
    <>
      <Dialog open={client !== null} onOpenChange={onOpenChange}>
        <DialogContent className="sm:max-w-md">
          {client ? (
            <>
              <DialogHeader>
                <DialogTitle>Клиент</DialogTitle>
              </DialogHeader>
              <form
                className="grid gap-3"
                onSubmit={(e) => {
                  e.preventDefault();
                  if (!client) return;

                  const unchanged =
                    name === client.name &&
                    description === (client.description ?? "");

                  if (unchanged) {
                    onOpenChange(false);
                    return;
                  }

                  void (async () => {
                    const saved = await onSave?.({ name, description });
                    if (saved) {
                      onOpenChange(false);
                    }
                  })();
                }}
              >
                <div className="grid gap-2">
                  <Label htmlFor="info-name">Имя</Label>
                  <Input
                    id="info-name"
                    required
                    maxLength={64}
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    disabled={pending}
                  />
                </div>
                <div className="grid gap-2">
                  <Label htmlFor="info-description">
                    Описание{" "}
                    <span className="font-normal text-muted-foreground">(опционально)</span>
                  </Label>
                  <Textarea
                    id="info-description"
                    rows={1}
                    value={description}
                    onChange={(e) => setDescription(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") {
                        e.stopPropagation();
                      }
                    }}
                    disabled={pending}
                  />
                </div>
                <dl className="grid gap-2 text-sm">
                  <div>
                    <dt className="text-muted-foreground">Статус</dt>
                    <dd>
                      {!client.enabled
                        ? "пауза"
                        : client.online
                          ? "онлайн"
                          : "офлайн"}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">IP</dt>
                    <dd className="font-mono">{client.address}</dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">Handshake</dt>
                    <dd>{formatHandshake(client.last_handshake_utc)}</dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">Трафик</dt>
                    <dd>
                      ↓ {formatBytes(client.rx_bytes)} · ↑ {formatBytes(client.tx_bytes)}
                    </dd>
                  </div>
                </dl>
                <div className="flex flex-wrap gap-2">
                  <Button type="button" variant="outline" disabled={pending} onClick={onQr}>
                    QR-код
                  </Button>
                  <Button type="button" variant="outline" disabled={pending} onClick={onDownload}>
                    Скачать конфиг
                  </Button>
                  <Button type="button" variant="outline" disabled={pending} onClick={onToggle}>
                    {client.enabled ? "Отключить" : "Включить"}
                  </Button>
                  <Button
                    type="button"
                    variant="destructive"
                    disabled={pending}
                    onClick={() => setConfirmOpen(true)}
                  >
                    Удалить
                  </Button>
                </div>
                <DialogFooter>
                  <Button type="submit" disabled={pending}>
                    Сохранить
                  </Button>
                </DialogFooter>
              </form>
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

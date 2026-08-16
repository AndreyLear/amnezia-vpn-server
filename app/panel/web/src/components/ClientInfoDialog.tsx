import { useEffect, useState } from "react";

import { ClientMenu, ClientPills } from "@/components/ClientCard";
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
import type { Client } from "@/lib/api";
import { formatBytes, formatHandshake } from "@/lib/format";

type ClientInfoDialogProps = {
  client: Client | null;
  pending?: boolean;
  onOpenChange: (open: boolean) => void;
  onSave?: (payload: { name: string; description: string }) => void | Promise<void>;
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
  }, [client]);

  return (
    <>
      <Dialog open={client !== null} onOpenChange={onOpenChange}>
        <DialogContent className="sm:max-w-md">
          {client ? (
            <>
              <DialogHeader className="flex-row items-start justify-between gap-2">
                <DialogTitle>{client.name}</DialogTitle>
                <ClientMenu
                  client={client}
                  pending={pending}
                  onInfo={() => {}}
                  onQr={onQr}
                  onDownload={onDownload}
                  onToggle={onToggle}
                  onDelete={() => setConfirmOpen(true)}
                />
              </DialogHeader>
              <form
                className="grid gap-3"
                onSubmit={(e) => {
                  e.preventDefault();
                  void onSave?.({ name, description });
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
                  <Label htmlFor="info-description">Описание</Label>
                  <Input
                    id="info-description"
                    value={description}
                    onChange={(e) => setDescription(e.target.value)}
                    disabled={pending}
                  />
                </div>
                <dl className="grid gap-2 text-sm">
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
                <ClientPills client={client} />
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
            <AlertDialogTitle>
              Удалить клиента «{client?.name}»?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Конфигурация клиента будет убрана из awg0.conf. Действие необратимо.
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

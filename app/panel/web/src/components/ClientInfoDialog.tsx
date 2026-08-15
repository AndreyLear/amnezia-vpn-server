import { useState } from "react";

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
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { Client } from "@/lib/api";
import { formatBytes, formatHandshake } from "@/lib/format";

type ClientInfoDialogProps = {
  client: Client | null;
  pending?: boolean;
  onOpenChange: (open: boolean) => void;
  onQr?: () => void;
  onDownload?: () => void;
  onToggle?: () => void;
  onDelete?: () => void;
};

export function ClientInfoDialog({
  client,
  pending,
  onOpenChange,
  onQr,
  onDownload,
  onToggle,
  onDelete,
}: ClientInfoDialogProps) {
  const [confirmOpen, setConfirmOpen] = useState(false);

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
              <dl className="grid gap-2 text-sm">
                <div>
                  <dt className="text-muted-foreground">Описание</dt>
                  <dd>{client.description || "—"}</dd>
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
              <ClientPills client={client} />
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

import { useState } from "react";
import { MoreHorizontalIcon, PlusIcon } from "lucide-react";

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
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { Client } from "@/lib/api";
import { formatBytes } from "@/lib/format";

export type ClientActions = {
  onInfo?: () => void;
  onQr?: () => void;
  onDownload?: () => void;
  onToggle?: () => void;
  onDelete?: () => void;
  pending?: boolean;
};

export function AddClientCard({ onClick }: { onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex h-full w-full min-h-36 flex-col items-center justify-center gap-2 rounded-xl bg-card text-sm text-muted-foreground ring-1 ring-foreground/10 transition-colors hover:bg-muted/40"
    >
      <PlusIcon className="size-6" />
      Добавить клиента
    </button>
  );
}

export function ClientMenu({
  client,
  onInfo,
  onQr,
  onDownload,
  onToggle,
  onDelete,
  pending,
}: { client: Client } & ClientActions) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          disabled={pending}
          aria-label={`Действия для ${client.name}`}
        >
          <MoreHorizontalIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={onInfo}>Сведения</DropdownMenuItem>
        <DropdownMenuItem onClick={onQr}>QR-код</DropdownMenuItem>
        <DropdownMenuItem onClick={onDownload}>Скачать конфиг</DropdownMenuItem>
        <DropdownMenuItem onClick={onToggle} disabled={pending}>
          {client.enabled ? "Отключить" : "Включить"}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onClick={onDelete} disabled={pending}>
          Удалить
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function ClientPills({ client }: { client: Client }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      <Badge
        variant={client.online ? "secondary" : "destructive"}
        className={
          client.online
            ? "bg-emerald-600/15 text-emerald-700 dark:text-emerald-400"
            : undefined
        }
      >
        {client.online ? "онлайн" : "офлайн"}
      </Badge>
      <Badge variant="outline">{formatBytes(client.rx_bytes)}</Badge>
      <Badge variant="outline">{formatBytes(client.tx_bytes)}</Badge>
    </div>
  );
}

export function ClientCard({
  client,
  onInfo,
  onQr,
  onDownload,
  onToggle,
  onDelete,
  pending,
}: { client: Client } & ClientActions) {
  const [confirmOpen, setConfirmOpen] = useState(false);

  return (
    <Card className="h-full min-h-36">
      <CardHeader className="border-b">
        <CardTitle>{client.name}</CardTitle>
        <CardAction>
          <ClientMenu
            client={client}
            pending={pending}
            onInfo={onInfo}
            onQr={onQr}
            onDownload={onDownload}
            onToggle={onToggle}
            onDelete={() => setConfirmOpen(true)}
          />
        </CardAction>
      </CardHeader>
      <CardContent>
        <ClientPills client={client} />
      </CardContent>
      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Удалить клиента «{client.name}»?</AlertDialogTitle>
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
    </Card>
  );
}

import { useState } from "react";
import { ArrowDownIcon, ArrowUpIcon, HandshakeIcon, MoreVerticalIcon } from "lucide-react";

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
import { Card, CardContent } from "@/components/ui/card";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import type { Client } from "@/lib/api";
import { formatBytes, formatHandshakeAge } from "@/lib/format";
import { cn } from "@/lib/utils";

export type ClientActions = {
  onInfo?: () => void;
  onQr?: () => void;
  onDownload?: () => void;
  onToggle?: () => void;
  onDelete?: () => void;
  pending?: boolean;
};

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
          onClick={(event) => event.stopPropagation()}
        >
          <MoreVerticalIcon />
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
    <Card
      className={cn(
        "relative client-card-sweep flex-row items-center hover:ring-foreground/25 sm:col-span-full sm:grid sm:grid-cols-subgrid py-1",
        !client.enabled && "opacity-60",
      )}
    >
      <CardContent className="flex min-w-0 flex-1 items-center max-sm:grid max-sm:grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-1 sm:col-span-full sm:grid sm:grid-cols-subgrid">
        <div className="flex min-w-0 flex-1 items-center gap-2">
          <button
            type="button"
            className="min-w-0 truncate text-left font-heading text-base font-medium"
            onClick={onInfo}
          >
            {client.name}
          </button>
          {!client.enabled ? (
            <span className="shrink-0 text-muted-foreground">Пауза</span>
          ) : null}
        </div>
        <TooltipProvider>
          <div className="flex shrink-0 items-center gap-3 text-muted-foreground max-sm:col-span-2 sm:contents">
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="inline-flex items-center gap-[0.4rem]">
                  <HandshakeIcon
                    className={cn("size-4", client.online && "text-emerald-500")}
                    aria-hidden
                  />
                  <span className="tabular-nums">{formatHandshakeAge(client.last_handshake_utc)}</span>
                </span>
              </TooltipTrigger>
              <TooltipContent>Последний handshake</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="inline-flex items-center gap-[0.25rem]">
                  <ArrowDownIcon className="size-4" aria-hidden />
                  <span className="tabular-nums">{formatBytes(client.rx_bytes)}</span>
                </span>
              </TooltipTrigger>
              <TooltipContent>Входящий трафик</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="inline-flex items-center gap-[0.25rem]">
                  <ArrowUpIcon className="size-4" aria-hidden />
                  <span className="tabular-nums">{formatBytes(client.tx_bytes)}</span>
                </span>
              </TooltipTrigger>
              <TooltipContent>Исходящий трафик</TooltipContent>
            </Tooltip>
          </div>
          <div className="max-sm:col-start-2 max-sm:row-start-1">
            <ClientMenu
              client={client}
              pending={pending}
              onInfo={onInfo}
              onQr={onQr}
              onDownload={onDownload}
              onToggle={onToggle}
              onDelete={() => setConfirmOpen(true)}
            />
          </div>
        </TooltipProvider>
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

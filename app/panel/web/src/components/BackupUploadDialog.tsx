import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import { api, mutationSucceeded, type MutationResponse } from "@/lib/api";
import { isBackupArchiveName } from "@/lib/backupArchive";

const ACCEPT = ".tar.zst";
const rejectCopy = "Нужен файл бэкапа (.tar.zst).";

type BackupUploadDialogProps = {
  open: boolean;
  restorePending?: boolean;
  onOpenChange: (open: boolean) => void;
  onPrepared?: () => void;
};

export function BackupUploadDialog({
  open,
  restorePending = false,
  onOpenChange,
  onPrepared,
}: BackupUploadDialogProps) {
  const [file, setFile] = useState<File | null>(null);
  const [pending, setPending] = useState(false);
  const [dragOver, setDragOver] = useState(false);

  useEffect(() => {
    if (!open) return;

    function onDragOver(e: DragEvent) {
      e.preventDefault();
      setDragOver(true);
    }

    function onDrop(e: DragEvent) {
      e.preventDefault();
      setDragOver(false);
      takeDropped(e.dataTransfer?.files[0]);
    }

    function clearDragOver() {
      setDragOver(false);
    }

    document.addEventListener("dragover", onDragOver);
    document.addEventListener("drop", onDrop);
    document.addEventListener("dragend", clearDragOver);
    return () => {
      document.removeEventListener("dragover", onDragOver);
      document.removeEventListener("drop", onDrop);
      document.removeEventListener("dragend", clearDragOver);
    };
  }, [open]);

  function takeDropped(next: File | undefined) {
    if (!next) return;
    if (!isBackupArchiveName(next.name)) {
      toast.error(rejectCopy);
      return;
    }
    setFile(next);
  }

  async function upload(next: File) {
    setPending(true);
    try {
      const body = new FormData();
      body.append("backup", next);
      const data = await api<MutationResponse>("/api/backups/restore", {
        method: "POST",
        body,
      });
      if (!mutationSucceeded(data)) {
        toast.error(data?.message ?? "Восстановление не удалось.");
        return;
      }
      toast.success(data.message ?? "Бэкап подготовлен. Требуется перезапуск.");
      onPrepared?.();
      setFile(null);
    } finally {
      setPending(false);
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          setFile(null);
          setDragOver(false);
        }
        onOpenChange(next);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Загрузить бэкап</DialogTitle>
        </DialogHeader>
        {restorePending ? (
          <p className="text-sm text-muted-foreground">
            Восстановление уже подготовлено. Требуется перезапуск.
          </p>
        ) : null}
        <form
          className="grid gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (file) void upload(file);
          }}
        >
          <label
            className={`grid cursor-pointer gap-2 rounded-lg border border-dashed p-6 text-center text-sm ${
              dragOver ? "border-primary bg-muted" : "border-border"
            }`}
            onDragOver={(e) => {
              e.preventDefault();
              e.stopPropagation();
              setDragOver(true);
            }}
            onDragLeave={() => setDragOver(false)}
            onDrop={(e) => {
              e.preventDefault();
              e.stopPropagation();
              setDragOver(false);
              takeDropped(e.dataTransfer.files[0]);
            }}
          >
            <span>Перетащите файл сюда или выберите на диске</span>
            <span className="text-muted-foreground">
              {file ? file.name : "Формат: .tar.zst"}
            </span>
            <input
              type="file"
              accept={ACCEPT}
              className="sr-only"
              disabled={pending || restorePending}
              onChange={(e) => {
                const next = e.target.files?.[0];
                if (!next) {
                  setFile(null);
                  return;
                }
                takeDropped(next);
                e.target.value = "";
              }}
            />
          </label>
          <DialogFooter>
            <Button type="submit" disabled={pending || restorePending || !file}>
              {pending ? <Spinner data-icon="inline-start" /> : null}
              Загрузить
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

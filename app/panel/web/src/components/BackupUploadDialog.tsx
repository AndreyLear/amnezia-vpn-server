import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api, mutationSucceeded, type MutationResponse } from "@/lib/api";

const ACCEPT = ".tar.zst,.zst";

type BackupUploadDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
};

export function BackupUploadDialog({
  open,
  onOpenChange,
}: BackupUploadDialogProps) {
  const [file, setFile] = useState<File | null>(null);
  const [pending, setPending] = useState(false);
  const [dragOver, setDragOver] = useState(false);

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
      setFile(null);
      onOpenChange(false);
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
              setDragOver(true);
            }}
            onDragLeave={() => setDragOver(false)}
            onDrop={(e) => {
              e.preventDefault();
              setDragOver(false);
              const dropped = e.dataTransfer.files[0];
              if (dropped) setFile(dropped);
            }}
          >
            <span>Перетащите файл сюда или выберите на диске</span>
            <span className="text-muted-foreground">
              {file ? file.name : "Формат: .tar.zst, .zst"}
            </span>
            <input
              type="file"
              accept={ACCEPT}
              className="sr-only"
              disabled={pending}
              onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            />
          </label>
          <DialogFooter>
            <Button type="submit" disabled={pending || !file}>
              Загрузить
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

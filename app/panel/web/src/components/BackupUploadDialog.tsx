import { useEffect, useRef, useState } from "react";
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
const SM_MIN_WIDTH = "(min-width: 640px)";
export const DROPZONE_MISS_MS = 1500;

function isMinWidthSm(): boolean {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return true;
  }
  return window.matchMedia(SM_MIN_WIDTH).matches;
}

function useMinWidthSm() {
  const [matches, setMatches] = useState(isMinWidthSm);

  useEffect(() => {
    if (typeof window.matchMedia !== "function") {
      return;
    }
    const mq = window.matchMedia(SM_MIN_WIDTH);
    const onChange = () => setMatches(mq.matches);
    onChange();
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);

  return matches;
}

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
  const [miss, setMiss] = useState(false);
  const isSmUp = useMinWidthSm();
  const inputRef = useRef<HTMLInputElement>(null);
  const missTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  function clearMiss() {
    if (missTimerRef.current !== null) {
      clearTimeout(missTimerRef.current);
      missTimerRef.current = null;
    }
    setMiss(false);
  }

  function flashMiss() {
    if (missTimerRef.current !== null) {
      clearTimeout(missTimerRef.current);
    }
    setMiss(true);
    missTimerRef.current = setTimeout(() => {
      missTimerRef.current = null;
      setMiss(false);
    }, DROPZONE_MISS_MS);
  }

  useEffect(() => {
    return () => {
      if (missTimerRef.current !== null) {
        clearTimeout(missTimerRef.current);
      }
    };
  }, []);

  useEffect(() => {
    if (!open || !isSmUp) return;

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
  }, [open, isSmUp]);

  function takeDropped(next: File | undefined) {
    if (!next) return;
    if (!isBackupArchiveName(next.name)) {
      toast.error(rejectCopy);
      return;
    }
    setFile(next);
    clearMiss();
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
      onOpenChange(false);
    } finally {
      setPending(false);
    }
  }

  const fileInput = (
    <input
      ref={inputRef}
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
  );

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          setFile(null);
          setDragOver(false);
          clearMiss();
        }
        onOpenChange(next);
      }}
    >
      <DialogContent className="gap-6 overflow-hidden sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Загрузить бэкап</DialogTitle>
        </DialogHeader>
        <form
          className="grid gap-6"
          onSubmit={(e) => {
            e.preventDefault();
            if (!file) {
              flashMiss();
              return;
            }
            void upload(file);
          }}
        >
          <div className="grid gap-4">
            {isSmUp ? (
              <label
                className={`grid min-w-0 cursor-pointer gap-2 overflow-hidden rounded-lg border border-dashed p-6 text-center text-sm transition-colors duration-500 motion-reduce:duration-0 ${
                  dragOver
                    ? "border-primary bg-muted"
                    : miss
                      ? "border-destructive"
                      : "border-border hover:border-input"
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
                <span
                  className="block min-w-0 truncate text-muted-foreground"
                  title={file?.name}
                >
                  {file ? file.name : "Формат: .tar.zst"}
                </span>
                {fileInput}
              </label>
            ) : (
              <div className="grid min-w-0 gap-4 overflow-hidden">
                <Button
                  type="button"
                  variant="outline"
                  className={`max-sm:h-12 max-sm:w-full transition-colors duration-500 ${
                    miss ? "border-destructive" : ""
                  }`}
                  disabled={pending || restorePending}
                  onClick={() => inputRef.current?.click()}
                >
                  Выберите файл на устройстве
                </Button>
                <span
                  className="block min-w-0 truncate text-muted-foreground"
                  title={file?.name}
                >
                  {file ? file.name : "Формат: .tar.zst"}
                </span>
                {fileInput}
              </div>
            )}
          </div>
          <DialogFooter>
            <Button
              type="submit"
              className="max-sm:h-12 max-sm:w-full"
              disabled={pending || restorePending}
            >
              {pending ? <Spinner data-icon="inline-start" /> : null}
              Загрузить
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

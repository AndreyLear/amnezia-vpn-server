import { createContext, useContext, useEffect, useState } from "react";
import type { ReactNode } from "react";
import { ChevronDownIcon, Download, Upload } from "lucide-react";

import { BackupUploadDialog } from "@/components/BackupUploadDialog";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { apiRequest } from "@/lib/api";
import { toast } from "sonner";

const SM_MIN_WIDTH = "(min-width: 640px)";

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

function filenameFromDisposition(header: string | null): string {
  if (!header) return "backup.tar.zst";
  const match = /filename="([^"]+)"/.exec(header);
  return match?.[1] ?? "backup.tar.zst";
}

type BackupMenuApi = {
  download: () => Promise<void>;
  setUploadOpen: (open: boolean) => void;
};

const BackupMenuContext = createContext<BackupMenuApi | null>(null);

function useBackupMenuState(restorePending: boolean) {
  const [uploadOpen, setUploadOpen] = useState(false);
  const [blocked, setBlocked] = useState(restorePending);

  useEffect(() => {
    setBlocked(restorePending);
  }, [restorePending]);

  async function download() {
    const res = await apiRequest("/api/backups/download", { method: "POST" });
    if (!res.ok) {
      let message = "Не удалось скачать бэкап.";
      try {
        const data = (await res.json()) as { message?: string };
        if (data.message) message = data.message;
      } catch {
        // keep generic
      }
      toast.error(message);
      return;
    }
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = filenameFromDisposition(res.headers.get("Content-Disposition"));
    a.click();
    URL.revokeObjectURL(url);
  }

  return {
    api: { download, setUploadOpen },
    uploadOpen,
    setUploadOpen,
    blocked,
    setBlocked,
  };
}

function BackupItems({ api }: { api: BackupMenuApi }) {
  return (
    <>
      <DropdownMenuItem onSelect={() => void api.download()}>Скачать</DropdownMenuItem>
      <DropdownMenuItem onSelect={() => api.setUploadOpen(true)}>Загрузить</DropdownMenuItem>
    </>
  );
}

export function BackupProvider({
  restorePending = false,
  children,
}: {
  restorePending?: boolean;
  children: ReactNode;
}) {
  const { api, uploadOpen, setUploadOpen, blocked, setBlocked } =
    useBackupMenuState(restorePending);

  return (
    <BackupMenuContext.Provider value={api}>
      {children}
      <BackupUploadDialog
        open={uploadOpen}
        restorePending={blocked}
        onOpenChange={setUploadOpen}
        onPrepared={() => setBlocked(true)}
      />
    </BackupMenuContext.Provider>
  );
}

function useBackupMenuApi(): BackupMenuApi {
  const ctx = useContext(BackupMenuContext);
  if (!ctx) {
    throw new Error("BackupMenu must be used within BackupProvider");
  }
  return ctx;
}

function BackupHeaderTrigger() {
  const api = useBackupMenuApi();
  const isSmUp = useMinWidthSm();
  const [sheetOpen, setSheetOpen] = useState(false);

  if (isSmUp) {
    return (
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="outline">
            Бэкап
            <ChevronDownIcon
              data-icon="inline-end"
              aria-hidden
              className="transition-transform group-aria-expanded/button:rotate-180"
            />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          onCloseAutoFocus={(event) => event.preventDefault()}
        >
          <BackupItems api={api} />
        </DropdownMenuContent>
      </DropdownMenu>
    );
  }

  return (
    <>
      <Button variant="outline" onClick={() => setSheetOpen(true)}>
        Бэкап
      </Button>
      <Dialog open={sheetOpen} onOpenChange={setSheetOpen}>
        <DialogContent className="gap-6">
          <DialogHeader>
            <DialogTitle>Бэкап</DialogTitle>
          </DialogHeader>
          <div className="flex flex-col gap-4">
            <Button
              type="button"
              variant="outline"
              className="max-sm:h-12 max-sm:w-full"
              onClick={() => void api.download()}
            >
              <Download data-icon="inline-start" />
              Скачать
            </Button>
            <Button
              type="button"
              variant="outline"
              className="max-sm:h-12 max-sm:w-full"
              onClick={() => {
                setSheetOpen(false);
                api.setUploadOpen(true);
              }}
            >
              <Upload data-icon="inline-start" />
              Загрузить
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}

export function BackupMenu({ restorePending = false }: { restorePending?: boolean }) {
  const ctx = useContext(BackupMenuContext);
  if (ctx) {
    return <BackupHeaderTrigger />;
  }
  return (
    <BackupProvider restorePending={restorePending}>
      <BackupHeaderTrigger />
    </BackupProvider>
  );
}

function BackupUploadTrigger() {
  const api = useBackupMenuApi();
  return (
    <Button type="button" variant="outline" onClick={() => api.setUploadOpen(true)}>
      Загрузить бэкап
    </Button>
  );
}

export function BackupUploadButton({ restorePending = false }: { restorePending?: boolean }) {
  const ctx = useContext(BackupMenuContext);
  if (ctx) {
    return <BackupUploadTrigger />;
  }
  return (
    <BackupProvider restorePending={restorePending}>
      <BackupUploadTrigger />
    </BackupProvider>
  );
}

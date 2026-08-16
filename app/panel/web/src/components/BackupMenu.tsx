import { createContext, useContext, useEffect, useState } from "react";
import type { ReactNode } from "react";
import { ChevronDownIcon } from "lucide-react";

import { BackupUploadDialog } from "@/components/BackupUploadDialog";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { apiRequest } from "@/lib/api";
import { toast } from "sonner";

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

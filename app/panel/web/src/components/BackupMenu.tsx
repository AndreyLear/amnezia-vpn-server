import { useState } from "react";

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

export function BackupMenu() {
  const [uploadOpen, setUploadOpen] = useState(false);

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

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="outline">Бэкап</Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onSelect={() => void download()}>
            Скачать
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => setUploadOpen(true)}>
            Загрузить
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <BackupUploadDialog open={uploadOpen} onOpenChange={setUploadOpen} />
    </>
  );
}

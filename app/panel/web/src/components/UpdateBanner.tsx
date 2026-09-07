import { useState } from "react";
import { XIcon } from "lucide-react";

import { UpdateDialog } from "@/components/UpdateDialog";
import { Button } from "@/components/ui/button";
import { api, mutationOk, type MutationResponse, type UpdateInfo } from "@/lib/api";

/**
 * Полоса о новом выпуске (amnezia-vpn-server-tjoq).
 *
 * Появляется, когда есть что взять, и прячется крестиком до следующего
 * выпуска. Закрытие хранится на сервере, а не в браузере: владелец один и тот
 * же на компьютере и на телефоне, и закрыть полосу дважды — это лишняя работа
 * без причины.
 *
 * Значок-напоминание на кнопке меню при этом остаётся: он гаснет, когда
 * версия обновлена, а не когда полосу убрали с глаз.
 */
export function UpdateBanner({
  info,
  onChanged,
}: {
  info: UpdateInfo | null;
  onChanged: () => void;
}) {
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [hiding, setHiding] = useState(false);

  if (!info?.available) return null;
  if (info.dismissed === info.latest) return null;

  async function dismiss() {
    if (!info || hiding) return;
    setHiding(true);
    const data = await api<MutationResponse>("/api/update/dismiss", {
      method: "POST",
      body: JSON.stringify({ version: info.latest }),
    });
    if (mutationOk(data)) onChanged();
    setHiding(false);
  }

  return (
    <>
      <div className="mt-4 flex items-center gap-3 rounded-lg border border-border bg-card px-4 py-3">
        <p className="min-w-0 flex-1">Вышла версия {info.latest}</p>
        <Button type="button" variant="outline" size="sm" onClick={() => setDetailsOpen(true)}>
          Показать подробности
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label="Скрыть до следующего выпуска"
          disabled={hiding}
          onClick={() => void dismiss()}
        >
          <XIcon />
        </Button>
      </div>
      <UpdateDialog
        info={info}
        open={detailsOpen}
        onOpenChange={setDetailsOpen}
        onStarted={onChanged}
      />
    </>
  );
}

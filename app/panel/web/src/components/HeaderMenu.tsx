import { useRef, useState } from "react";
import { MoreHorizontalIcon } from "lucide-react";
import { toast } from "sonner";

import { AboutDialog } from "@/components/AboutDialog";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { api, mutationOk, type MutationResponse } from "@/lib/api";

/**
 * Меню в шапке (amnezia-vpn-server-8bt5).
 *
 * Пункты живут списком, а не вёрсткой: следующий добавляется строкой здесь и
 * ничего в шапке не трогает. Ничего существующего внутрь не переносим —
 * «Бэкап» частое действие, и убрать его вглубь меню было бы ухудшением, даже
 * если выглядит стройнее.
 */
type MenuItem = {
  label: string;
  onSelect: () => void;
};

export function HeaderMenu({ pendingUpdate = false }: { pendingUpdate?: boolean }) {
  const [aboutOpen, setAboutOpen] = useState(false);
  const [checking, setChecking] = useState(false);
  // Radix возвращает фокус на кнопку при закрытии меню. Для клавиатуры это
  // единственно верно; для мыши кольцо фокуса остаётся гореть, будто меню всё
  // ещё открыто (amnezia-vpn-server-c7iz).
  const openedByKeyboard = useRef(false);

  async function checkForUpdates() {
    if (checking) return;
    setChecking(true);
    const data = await api<MutationResponse>("/api/update/check", { method: "POST" });
    // Проверку делает хост, и ответ появится в файле через секунду-другую.
    // Обещать результат немедленно значило бы соврать.
    if (mutationOk(data)) toast.success("Проверяю обновления");
    setChecking(false);
  }

  const items: MenuItem[] = [
    { label: "О версиях", onSelect: () => setAboutOpen(true) },
    { label: "Проверить обновления", onSelect: () => void checkForUpdates() },
  ];

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            type="button"
            variant="outline"
            size="icon"
            aria-label="Ещё"
            className="relative"
            onPointerDown={() => {
              openedByKeyboard.current = false;
            }}
            onKeyDown={(event) => {
              if (event.key === "Enter" || event.key === " ") {
                openedByKeyboard.current = true;
              }
            }}
          >
            <MoreHorizontalIcon />
            {pendingUpdate ? (
              // Напоминание о невзятом выпуске. Гаснет, когда версия
              // обновлена, — не когда полосу закрыли крестиком.
              <span
                data-testid="update-badge"
                aria-label="Вышел новый выпуск"
                className="absolute -end-0.5 -top-0.5 size-2.5 rounded-full bg-destructive ring-2 ring-background"
              />
            ) : null}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          onCloseAutoFocus={(event) => {
            if (!openedByKeyboard.current) {
              event.preventDefault();
            }
          }}
        >
          {items.map((item) => (
            <DropdownMenuItem key={item.label} onSelect={item.onSelect}>
              {item.label}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
      <AboutDialog open={aboutOpen} onOpenChange={setAboutOpen} />
    </>
  );
}

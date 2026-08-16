import type { ReactNode } from "react";
import { useState } from "react";
import { EllipsisVerticalIcon, PlusIcon } from "lucide-react";

import { AmbientBackground } from "@/components/AmbientBackground";
import { BackupMenu } from "@/components/BackupMenu";
import { PasswordDialog } from "@/components/PasswordDialog";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { api } from "@/lib/api";
import { applyTheme, getTheme, type Theme } from "@/lib/theme";

export function AppShell({
  children,
  totpEnabled,
  restorePending = false,
  onTotpChange,
  onAddClient,
}: {
  children: ReactNode;
  totpEnabled: boolean;
  restorePending?: boolean;
  onTotpChange: (enabled: boolean) => void;
  onAddClient: () => void;
}) {
  const [theme, setThemeState] = useState<Theme>(() =>
    typeof window === "undefined" ? "system" : getTheme(),
  );
  const [passwordOpen, setPasswordOpen] = useState(false);

  function chooseTheme(next: Theme) {
    setThemeState(next);
    applyTheme(next);
  }

  async function logout() {
    await api("/api/logout", { method: "POST" });
    window.location.assign("/login");
  }

  return (
    <div className="relative min-h-svh">
      <AmbientBackground />
      <div className="relative mx-auto w-full max-w-[752px] px-4">
        <header className="flex items-center gap-2 py-4">
          <p className="font-mono text-sm font-medium">AWG Panel</p>
          <div className="ml-auto flex items-center gap-2">
            <Button type="button" onClick={onAddClient}>
              <PlusIcon />
              Добавить клиента
            </Button>
            <BackupMenu restorePending={restorePending} />
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" aria-label="Меню">
                  <EllipsisVerticalIcon />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="min-w-56 w-auto">
                <DropdownMenuSub>
                  <DropdownMenuSubTrigger>Тема</DropdownMenuSubTrigger>
                  <DropdownMenuSubContent>
                    <DropdownMenuRadioGroup
                      value={theme}
                      onValueChange={(value) => chooseTheme(value as Theme)}
                    >
                      <DropdownMenuRadioItem value="light">Светлая</DropdownMenuRadioItem>
                      <DropdownMenuRadioItem value="dark">Тёмная</DropdownMenuRadioItem>
                      <DropdownMenuRadioItem value="system">Системная</DropdownMenuRadioItem>
                    </DropdownMenuRadioGroup>
                  </DropdownMenuSubContent>
                </DropdownMenuSub>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => setPasswordOpen(true)}>
                  Изменить пароль
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => void logout()}>Выйти</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </header>
        {children}
      </div>
      <PasswordDialog
        open={passwordOpen}
        totpEnabled={totpEnabled}
        onOpenChange={setPasswordOpen}
        onTotpChange={onTotpChange}
      />
    </div>
  );
}

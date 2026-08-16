import type { ReactNode } from "react";
import { useState } from "react";
import { EllipsisVerticalIcon, PlusIcon } from "lucide-react";

import { AmbientBackground } from "@/components/AmbientBackground";
import { BackupMenu, BackupOverflowSub, BackupProvider } from "@/components/BackupMenu";
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
        <header className="flex min-w-0 items-center gap-2 pt-4">
          <p className="min-w-0 truncate font-mono text-base font-medium">AWG Panel</p>
          <div className="ml-auto flex shrink-0 items-center gap-2">
            <Button
              type="button"
              aria-label="Добавить клиента"
              className="hidden sm:inline-flex"
              onClick={onAddClient}
            >
              <PlusIcon />
              <span className="sm:inline">Добавить клиента</span>
            </Button>
            <BackupProvider restorePending={restorePending}>
              <BackupMenu restorePending={restorePending} />
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="outline" size="icon" aria-label="Меню">
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
                  <BackupOverflowSub />
                  <DropdownMenuSeparator />
                  <DropdownMenuItem onSelect={() => setPasswordOpen(true)}>
                    Аккаунт
                  </DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => void logout()}>Выйти</DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </BackupProvider>
          </div>
        </header>
        {children}
      </div>
      <div className="pointer-events-none fixed inset-x-0 bottom-0 z-20 sm:hidden">
        <div className="pointer-events-none absolute inset-x-0 bottom-0 h-24 bg-gradient-to-t from-background to-transparent" />
        <div className="pointer-events-auto relative mx-auto w-full max-w-[752px] px-4 pb-6">
          <Button
            type="button"
            aria-label="Добавить клиента"
            className="h-12 w-full"
            onClick={onAddClient}
          >
            <PlusIcon />
            Добавить клиента
          </Button>
        </div>
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

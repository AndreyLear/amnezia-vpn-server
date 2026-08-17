import type { ReactNode } from "react";
import { useState } from "react";
import { Moon, PlusIcon, Sun } from "lucide-react";

import { AmbientBackground } from "@/components/AmbientBackground";
import { BackupMenu, BackupProvider } from "@/components/BackupMenu";
import { HeaderStats } from "@/components/HeaderStats";
import { Button } from "@/components/ui/button";
import type { HostSnapshot } from "@/lib/api";
import { applyTheme, getTheme, type Theme } from "@/lib/theme";

export function AppShell({
  children,
  restorePending = false,
  onAddClient,
  host = null,
}: {
  children: ReactNode;
  restorePending?: boolean;
  onAddClient: () => void;
  host?: HostSnapshot | null;
}) {
  const [theme, setThemeState] = useState<Theme>(() =>
    typeof window === "undefined" ? "dark" : getTheme(),
  );

  function toggleTheme() {
    const next: Theme = theme === "dark" ? "light" : "dark";
    setThemeState(next);
    applyTheme(next);
  }

  return (
    <div className="relative min-h-svh">
      <AmbientBackground />
      <div className="relative mx-auto w-full max-w-[752px] px-4">
        <header className="flex min-w-0 items-center gap-2 pt-4">
          <p className="shrink-0 font-mono text-base font-medium">AWG Panel</p>
          <HeaderStats host={host} className="flex min-w-0" />
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
              <Button
                type="button"
                variant="outline"
                size="icon"
                aria-label={theme === "dark" ? "Тёмная тема" : "Светлая тема"}
                onClick={toggleTheme}
              >
                {theme === "dark" ? <Moon /> : <Sun />}
              </Button>
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
    </div>
  );
}

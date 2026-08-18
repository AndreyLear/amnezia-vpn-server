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
  empty = false,
}: {
  children: ReactNode;
  restorePending?: boolean;
  onAddClient: () => void;
  host?: HostSnapshot | null;
  empty?: boolean;
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
      <AmbientBackground center={empty} />
      <BackupProvider restorePending={restorePending}>
        <div
          className={
            empty
              ? "relative mx-auto flex min-h-svh w-full max-w-[752px] flex-col items-center justify-center gap-8 px-4"
              : "relative mx-auto w-full max-w-[752px] px-4"
          }
        >
          <header
            className={
              empty
                ? "flex min-w-0 items-center justify-center"
                : "flex min-w-0 items-center pt-4 sm:pt-8"
            }
          >
            <img
              src="/favicon.svg"
              alt="AWG Panel"
              width={24}
              height={24}
              className="size-6 shrink-0"
            />
            {empty ? null : (
              <>
                <HeaderStats host={host} className="ms-3 flex min-w-0" />
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
                </div>
              </>
            )}
          </header>
          {children}
        </div>
        {empty ? null : (
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
        )}
      </BackupProvider>
    </div>
  );
}

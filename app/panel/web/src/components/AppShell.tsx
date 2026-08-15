import type { ReactNode } from "react";

import { AmbientBackground } from "@/components/AmbientBackground";

export function AppShell({ children }: { children: ReactNode }) {
  return (
    <div className="relative min-h-svh">
      <AmbientBackground />
      <div className="relative mx-auto w-full max-w-[752px] px-4">
        <header className="flex items-center py-4">
          <p className="text-sm font-medium">AWG Panel</p>
        </header>
        {children}
      </div>
    </div>
  );
}

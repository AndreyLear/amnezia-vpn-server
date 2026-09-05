import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";

// Подсказка висела над строкой клиента, принимала курсор на себя и закрывала
// соседние кнопки ровно тогда, когда человек к ним тянулся
// (amnezia-vpn-server-n9vi).
describe("Tooltip", () => {
  it("не удерживает себя под курсором и не перехватывает нажатия", () => {
    render(
      <TooltipProvider>
        <Tooltip open>
          <TooltipTrigger>наведи</TooltipTrigger>
          <TooltipContent>описание</TooltipContent>
        </Tooltip>
      </TooltipProvider>,
    );

    const content = screen.getByText("описание").closest('[data-slot="tooltip-content"]');
    expect(content).toBeTruthy();
    expect(content).toHaveClass("pointer-events-none");
  });

  // Одного pointer-events мало: Radix держит подсказку открытой, пока курсор
  // идёт к ней, и без этого флага она переживает уход с элемента. Проверяется
  // по исходнику, как инварианты index.css: свойство передаётся в примитив и
  // в разметке не отражается.
  it("объявляет содержимое ненаводимым на уровне примитива", () => {
    const source = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), "tooltip.tsx"),
      "utf8",
    );
    expect(source).toMatch(/disableHoverableContent/);
  });
});

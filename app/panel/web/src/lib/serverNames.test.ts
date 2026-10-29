import { describe, expect, it } from "vitest";

import { entryLabel, entryRef, serverButtonLabel } from "@/lib/serverNames";

// Подписи серверов (amnezia-vpn-server-q1do.4): без имени — как было,
// с именем — имя владельца, в предложениях — в кавычках после слова.
describe("serverNames", () => {
  it("подпись каскада: имя владельца, иначе «Каскад» или «Каскад <узел>»", () => {
    expect(entryLabel({ name: "entry" }, 1)).toBe("Каскад");
    expect(entryLabel({ name: "entry", title: "" }, 2)).toBe("Каскад entry");
    expect(entryLabel({ name: "entry", title: "Москва" }, 1)).toBe("Москва");
    expect(entryLabel({ name: "entry", title: "Москва" }, 3)).toBe("Москва");
  });

  it("каскад внутри фразы: имя в кавычках, без имени — техническое", () => {
    expect(entryRef({ name: "entry" })).toBe("entry");
    expect(entryRef({ name: "entry", title: "Дача 2" })).toBe("«Дача 2»");
  });

  it("имя кнопки основного сервера", () => {
    expect(serverButtonLabel("")).toBe("Сервер");
    expect(serverButtonLabel(undefined)).toBe("Сервер");
    expect(serverButtonLabel("Москва")).toBe("Сервер «Москва»");
  });
});

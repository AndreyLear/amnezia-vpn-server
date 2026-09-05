import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// Инварианты index.html пиннятся так же, как инварианты index.css: файл
// маленький, правится редко и ломает вещи, которых не видно в компонентах.
const html = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "..", "index.html"),
  "utf8",
);

describe("index.html", () => {
  it("объявляет русский язык, иначе браузер переводит имена клиентов", () => {
    expect(html).toMatch(/<html lang="ru">/);
    expect(html).not.toMatch(/<html lang="en">/);
  });

  it("несёт собственный заголовок, а не заводской vite", () => {
    const title = html.match(/<title>([^<]*)<\/title>/);
    expect(title).toBeTruthy();
    expect(title![1].trim()).not.toBe("web");
    expect(title![1].trim().length).toBeGreaterThan(0);
  });
});

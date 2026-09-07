import { expect, test, type Page } from "@playwright/test";

// Полоса о новом выпуске против настоящей панели (amnezia-vpn-server-tjoq,
// amnezia-vpn-server-8bt5). Снимок выпуска новее установленного кладёт
// фикстура: без него полосу можно было бы проверить только дождавшись
// настоящего выпуска.
const user = "e2e";
const password = "e2e-password-correct-horse";

async function login(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Имя пользователя").fill(user);
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByRole("button", { name: "Добавить клиента" })).toBeVisible();
}

test("полоса называет вышедшую версию", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page);
  await expect(page.getByText("Вышла версия 99.9.9")).toBeVisible();
});

test("подробности показывают изменения и предупреждают о перерыве", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page);
  await page.getByRole("button", { name: "Показать подробности" }).click();
  await expect(page.getByText("Первое изменение")).toBeVisible();
  await expect(page.getByText(/клиенты остаются без связи/)).toBeVisible();
  // Контрольная сумма предназначена агенту обновления, а не человеку.
  await expect(page.getByText(/amnezia-sha256/)).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Обновить" })).toBeVisible();
});

// Крестик прячет полосу до следующего выпуска, и это помнит сервер: другой
// браузер того же владельца должен увидеть то же самое.
test("закрытая полоса не возвращается после перезагрузки", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page);
  await page.getByRole("button", { name: "Скрыть до следующего выпуска" }).click();
  await expect(page.getByText("Вышла версия 99.9.9")).toHaveCount(0);

  await page.reload();
  await expect(page.getByRole("button", { name: "Добавить клиента" })).toBeVisible();
  await expect(page.getByText("Вышла версия 99.9.9")).toHaveCount(0);
  // А напоминание остаётся: оно гаснет, когда версия обновлена, а не когда
  // полосу убрали с глаз.
  await expect(page.getByLabel("Вышел новый выпуск")).toBeVisible();
});

test("окно «О версиях» не оставляет пустых мест", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page);
  await page.getByRole("button", { name: "Ещё" }).click();
  await page.getByRole("menuitem", { name: "О версиях" }).click();

  await expect(page.getByRole("heading", { name: "О версиях" })).toBeVisible();
  await expect(page.getByText("AmneziaWG 2.0")).toBeVisible();
  await expect(page.getByText("Схема базы")).toBeVisible();
  // Панель, запущенная без развёртывания, про хост не знает — и обязана
  // сказать именно это, а не оставить пустоту.
  await expect(page.getByText("неизвестно").first()).toBeVisible();
});

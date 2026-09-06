import { expect, test, type Page } from "@playwright/test";

// Экран первого запуска переделывали целой серией задач — и ни одна проверка
// e2e его не покрывала. Панель без клиентов это первое, что видит владелец
// после установки (amnezia-vpn-server-e72j).
const user = "e2e";
const password = "e2e-password-correct-horse";

async function login(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Имя пользователя").fill(user);
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти" }).click();
}

test("без клиентов показывает начальный экран, а не пустую сетку", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page);

  await expect(page.getByRole("button", { name: "Добавить клиента" })).toBeVisible();
  await expect(page.getByTestId("client-grid")).toHaveCount(0);
});

test("начальный экран даёт восстановить бэкап одним действием", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page);

  // На первом запуске скачивать нечего, поэтому здесь не меню, а одно
  // действие: загрузить бэкап.
  await page.getByRole("button", { name: "Бэкап" }).click();
  await expect(page.getByRole("heading", { name: "Загрузить бэкап" })).toBeVisible();
});

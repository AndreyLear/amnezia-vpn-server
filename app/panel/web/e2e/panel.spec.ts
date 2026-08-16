import { expect, test, type Page } from "@playwright/test";

const user = "e2e";
const password = "e2e-password-correct-horse";

async function login(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Имя пользователя").fill(user);
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByRole("button", { name: "Добавить клиента" })).toBeVisible();
  await expect(page.getByLabel("Код")).toHaveCount(0);
}

test("login has no AWG Panel brand", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 720 });
  await page.goto("/login");
  await expect(page.getByText("AWG Panel")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Войти" })).toBeVisible();
});

test("login shows the client grid", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page);
  await expect(page.getByTestId("client-grid")).toBeVisible();
  await expect(page.getByRole("button", { name: "Добавить клиента" })).toBeVisible();
});

test("opens the backup upload dialog", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page);
  await page.getByRole("button", { name: "Бэкап" }).click();
  await page.getByRole("menuitem", { name: "Загрузить" }).click();
  await expect(page.getByRole("heading", { name: "Загрузить бэкап" })).toBeVisible();
});

test("opens backup upload from the overflow menu at 375px", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 720 });
  await login(page);
  await page.getByRole("button", { name: "Бэкап" }).click();
  await page.getByRole("menuitem", { name: "Загрузить" }).click();
  await expect(page.getByRole("heading", { name: "Загрузить бэкап" })).toBeVisible();
});

test("752px and 375px keep the client list in one column", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page);
  await page.getByRole("button", { name: "Добавить клиента" }).click();
  await page.getByLabel("Имя").fill("e2e-client");
  await page.getByRole("button", { name: "Добавить" }).click();
  await expect(page.getByText("e2e-client")).toBeVisible();

  await page.setViewportSize({ width: 752, height: 800 });
  const wide = await page.locator('[data-testid="client-grid"] > *').evaluateAll((els) =>
    els.map((el) => el.getBoundingClientRect().x),
  );
  expect(wide.length).toBeGreaterThanOrEqual(1);
  expect(wide.every((x) => x === wide[0])).toBe(true);

  await page.setViewportSize({ width: 375, height: 720 });
  const narrow = await page.locator('[data-testid="client-grid"] > *').evaluateAll((els) =>
    els.map((el) => el.getBoundingClientRect().x),
  );
  expect(narrow.length).toBeGreaterThanOrEqual(1);
  expect(narrow.every((x) => x === narrow[0])).toBe(true);
});

test("unknown route shows не найдено", async ({ page }) => {
  await page.goto("/does-not-exist");
  await expect(page.getByText("не найдено")).toBeVisible();
});

test("overflow menu has no account item at 375px", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 720 });
  await login(page);

  await expect(page.getByRole("button", { name: "Меню" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Выйти" })).toHaveCount(0);
  await expect(page.getByText("Выйти")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Тёмная тема" })).toBeVisible();
});

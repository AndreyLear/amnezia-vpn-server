import { expect, test, type Page } from "@playwright/test";

const user = "e2e";
const password = "e2e-password-correct-horse";

async function login(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Имя пользователя").fill(user);
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByText("Добавить клиента")).toBeVisible();
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
  await expect(page.getByText("Добавить клиента")).toBeVisible();
});

test("opens the backup upload dialog", async ({ page }) => {
  await login(page);
  await page.getByRole("button", { name: "Бэкап" }).click();
  await page.getByRole("menuitem", { name: "Загрузить" }).click();
  await expect(page.getByRole("heading", { name: "Загрузить бэкап" })).toBeVisible();
});

test("752px two columns vs 375px one column", async ({ page }) => {
  await login(page);
  await page.getByText("Добавить клиента").click();
  await page.getByLabel("Имя").fill("e2e-client");
  await page.getByRole("button", { name: "Добавить" }).click();
  await expect(page.getByText("e2e-client")).toBeVisible();

  await page.setViewportSize({ width: 752, height: 800 });
  const wide = await page.locator('[data-testid="client-grid"] > *').evaluateAll((els) =>
    els.map((el) => el.getBoundingClientRect().x),
  );
  expect(wide.length).toBeGreaterThanOrEqual(2);
  expect(wide[0]).not.toBe(wide[1]);

  await page.setViewportSize({ width: 375, height: 720 });
  const narrow = await page.locator('[data-testid="client-grid"] > *').evaluateAll((els) =>
    els.map((el) => el.getBoundingClientRect().x),
  );
  expect(narrow[0]).toBe(narrow[1]);
});

test("unknown route shows не найдено", async ({ page }) => {
  await page.goto("/does-not-exist");
  await expect(page.getByText("не найдено")).toBeVisible();
});

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

// На узком экране «Бэкап» открывает не меню, а диалог с крупными кнопками:
// пункт меню в палец шириной с телефона не нажать. Проверка описывала прежнее
// устройство и падала, хотя панель работала (amnezia-vpn-server-e72j).
test("opens backup upload from the backup dialog at 375px", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 720 });
  await login(page);
  await page.getByRole("button", { name: "Бэкап" }).click();
  await expect(page.getByRole("heading", { name: "Бэкап" })).toBeVisible();
  await page.getByRole("button", { name: "Загрузить" }).click();
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

test("unknown route shows Не найдено", async ({ page }) => {
  await page.goto("/does-not-exist");
  await expect(page.getByText("Не найдено")).toBeVisible();
});

test("overflow menu has no account item at 375px", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 720 });
  await login(page);

  await expect(page.getByRole("button", { name: "Меню" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Выйти" })).toHaveCount(0);
  await expect(page.getByText("Выйти")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Тёмная тема" })).toBeVisible();
});

// График скорости в карточке клиента (amnezia-vpn-server-tmjw). Смысл —
// разбирать жалобу «не грузит видео», поэтому проверяется не наличие
// прямоугольника, а то, что провал в данных виден как разброс.
test("карточка клиента показывает график скорости", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page);
  await page.getByRole("button", { name: "alice", exact: true }).click();

  const chart = page.getByRole("img", { name: /Скорость/ });
  await expect(chart).toBeVisible();
  // Заливка приёма есть, и она разорвана там, где замеров не было: одна
  // сплошная фигура на весь график означала бы, что разрывы залиты нулём.
  await expect.poll(async () => chart.locator("path").count()).toBeGreaterThan(0);
  // Три линии сетки — то, по чему читаются значения.
  await expect.poll(async () => chart.locator("line.text-border").count()).toBe(3);
  // Пика под графиком больше нет: шкала следует за данными, и верх оси
  // называет почти то же число (amnezia-vpn-server-jyhb). Вместо него —
  // легенда, стоящая в одной строке с метками времени.
  await expect(page.getByText("Скачал")).toBeVisible();
  await expect(page.getByText("Отдал")).toBeVisible();
  // Ось: верх шкалы, середина, ноль.
  await expect(page.getByText("0", { exact: true })).toBeVisible();

  // Сутки — то же окно, другой охват; данные фикстуры лежат в последних
  // минутах, поэтому график остаётся непустым.
  await page.getByRole("button", { name: "Сутки" }).click();
  await expect(page.getByRole("button", { name: "Сутки" })).toHaveAttribute("aria-pressed", "true");
  await expect(chart).toBeVisible();

  // И обратно в короткое окно: час заменён десятью минутами
  // (amnezia-vpn-server-teos), и фикстура кладёт ровно этот отрезок.
  await page.getByRole("button", { name: "10 минут" }).click();
  await expect(page.getByRole("button", { name: "10 минут" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(chart).toBeVisible();
});

// Ширину меню юнит-тест не измерит: в jsdom вёрстки нет вовсе, там любая
// ширина равна нулю. Именно поэтому первый заход прошёл зелёным, а у
// владельца меню прыгало под курсором (amnezia-vpn-server-x65u).
test("ширина меню не меняется, пока идёт проверка обновлений", async ({ page }) => {
  await login(page);

  // Ширина берётся только после того, как она перестала меняться: меню
  // открывается с анимацией zoom-in-95, и замер в её середине даёт 95% от
  // настоящей ширины — тест падал бы на ровном месте.
  const menuWidth = async () => {
    let last = -1;
    for (let i = 0; i < 20; i++) {
      const box = await page.locator("[role=menu]").boundingBox();
      const w = Math.round(box!.width);
      if (w === last) return w;
      last = w;
      await page.waitForTimeout(50);
    }
    return last;
  };

  await page.getByRole("button", { name: "Ещё" }).click();
  const before = await menuWidth();

  await page.getByRole("menuitem", { name: /Проверить обновления|Доступна новая версия/ }).click();
  await expect(page.getByRole("menuitem", { name: "Проверяю" })).toBeVisible();
  expect(await menuWidth()).toBe(before);
});

// Уведомления не были видны около четырёх недель: политика безопасности
// отбрасывала стили, которые sonner вставляет в страницу сам, и уведомления
// уезжали под нижний край экрана. Проверки при этом зелёные: toBeVisible
// считает видимым и элемент за краем окна. Поэтому здесь меряется, что
// уведомление целиком внутри окна (amnezia-vpn-server-omsa).
test("уведомление видно на экране, а не уезжает под край", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await login(page);

  await page.getByRole("button", { name: /Действия для/ }).first().click();
  await page.getByRole("menuitem", { name: /Отключить|Включить/ }).click();

  const toast = page.locator("[data-sonner-toast]").first();
  await expect(toast).toBeAttached();
  await expect
    .poll(async () => {
      const box = await toast.boundingBox();
      const vp = page.viewportSize()!;
      return Boolean(box && box.y >= 0 && box.y + box.height <= vp.height && box.x >= 0 && box.x + box.width <= vp.width);
    })
    .toBe(true);
});

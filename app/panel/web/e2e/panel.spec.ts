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

// Окно «Уведомления» (amnezia-vpn-server-8fg2) в настоящей панели: пункт в
// меню открывает окно, сохранение доходит до сервера, пароль после него не
// виден, окно ждёт ответа службы писем. Службы в e2e нет, поэтому ответ не
// приходит — и это как раз состояние «ждём». Лежит здесь, а не в своём
// файле: файлы одного проекта идут параллельно, и вход тем же пользователем
// из соседнего файла выбивал сессию.
for (const width of [1280, 375]) {
  test(`окно уведомлений сохраняет настройки (${width}px)`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await login(page);
    await page.getByRole("button", { name: "Ещё" }).click();
    await page.getByRole("menuitem", { name: "Уведомления" }).click();
    const dialog = page.getByRole("dialog", { name: "Уведомления" });
    await expect(dialog).toBeVisible();

    await dialog.getByLabel("Сервер SMTP").fill("smtp.example.org");
    await dialog.getByLabel("Логин").fill("vpn@example.org");
    await dialog.getByLabel("Пароль").fill("mailbox-secret");
    await dialog.getByLabel("Куда присылать").fill("owner@example.org");
    await dialog.getByRole("button", { name: "Сохранить" }).click();

    await expect(dialog.getByText(/Отправляем пробное письмо/)).toBeVisible();
    await expect(dialog.getByLabel("Пароль")).toHaveValue("");
    await page.screenshot({ path: `test-results/notifications-${width}.png` });

    // Кнопки внутри экрана, а не под краем.
    const save = await dialog.getByRole("button", { name: "Сохранить" }).boundingBox();
    expect(save).not.toBeNull();
    expect(save!.x + save!.width).toBeLessThanOrEqual(width);

    // После перезагрузки настройки на месте, пароль по-прежнему не виден.
    await page.reload();
    await page.getByRole("button", { name: "Ещё" }).click();
    await page.getByRole("menuitem", { name: "Уведомления" }).click();
    await expect(dialog.getByLabel("Куда присылать")).toHaveValue("owner@example.org");
    await expect(dialog.getByLabel("Пароль")).toHaveValue("");
    expect(await page.content()).not.toContain("mailbox-secret");
  });
}

// Поймано на тестовом сервере (amnezia-vpn-server-2pdq): ответ с
// настройками пришёл позже ввода и затёр его. Локально ответ мгновенный,
// поэтому здесь он задерживается нарочно. Там же — поле порта съезжало вниз
// за текстом ошибки под сервером.
test("окно уведомлений не затирает ввод медленным ответом и держит строку ровной", async ({ page }) => {
  await page.route("**/api/mail", async (route) => {
    if (route.request().method() === "GET") await new Promise((r) => setTimeout(r, 1500));
    await route.continue();
  });
  await login(page);
  await page.getByRole("button", { name: "Ещё" }).click();
  await page.getByRole("menuitem", { name: "Уведомления" }).click();
  const dialog = page.getByRole("dialog", { name: "Уведомления" });
  // Ввод сразу после открытия, пока ответ ещё в пути.
  await dialog.getByLabel("Сервер SMTP").fill("smtp.slow.example.org");
  await page.waitForTimeout(2000);
  await expect(dialog.getByLabel("Сервер SMTP")).toHaveValue("smtp.slow.example.org");

  await dialog.getByLabel("Сервер SMTP").fill("");
  await dialog.getByRole("button", { name: "Сохранить" }).click();
  await expect(dialog.getByText("Укажите адрес почтового сервера")).toBeVisible();
  const hostBox = await dialog.getByLabel("Сервер SMTP").boundingBox();
  const portBox = await dialog.getByLabel("Порт").boundingBox();
  expect(Math.abs(hostBox!.y - portBox!.y)).toBeLessThanOrEqual(1);
  await page.screenshot({ path: "test-results/notifications-error-row.png" });
});

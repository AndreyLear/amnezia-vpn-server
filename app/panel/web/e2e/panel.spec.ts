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

// Трафик по дням — во вкладке протокола, под графиком скорости, как и он
// свой у AWG и у VLESS (amnezia-vpn-server-gzmb). История у свежей
// фикстуры пуста — это и проверяется: заглушка объясняет, откуда она
// возьмётся, и на 375px график не шире окна.
for (const width of [1280, 375]) test(`вкладки клиента показывают трафик по дням под скоростью (${width}px)`, async ({ page }) => {
  await page.setViewportSize({ width, height: 900 });
  await login(page);
  await expect(page.getByLabel("Протокол: AWG").first()).toBeVisible();
  if (width < 640) await page.getByText("alice", { exact: true }).click();
  else await page.getByRole("button", { name: "alice", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.locator('[data-slot="info-block"]')).toHaveCount(2);
  for (const tab of ["AWG", "VLESS"]) {
    await page.getByRole("tab", { name: tab }).click();
    const panel = page.getByRole("tabpanel");
    await expect(panel.getByText(/^Скорость/)).toBeVisible();
    const title = panel.getByText("Трафик по дням", { exact: true });
    await expect(title).toBeVisible();
    await expect(panel.getByText("За 30 дней трафика нет")).toBeVisible();
    await expect(panel.getByText("История копится с обновления панели")).toBeVisible();
    await expect(dialog.getByText(/^Трафик по дням/)).toHaveCount(1);
    const speedBox = await panel.getByText(/^Скорость/).boundingBox();
    const titleBox = await title.boundingBox();
    expect(speedBox && titleBox && titleBox.y > speedBox.y).toBe(true);
    const box = await panel.getByText("За 30 дней трафика нет").boundingBox();
    expect(box && box.x >= 0 && box.x + box.width <= width).toBe(true);
  }
});

// Закреплённая шапка красила фон только на высоту заголовка: sticky top-0
// отсчитывается от края отступа прокручиваемого тела, и в полосе над шапкой
// и по её бокам проезжало содержимое (amnezia-vpn-server-hscw). Проверяется
// вёрсткой: от верхнего края окна до низа шапки по всей ширине под точкой
// не должно оказаться ничего из прокручиваемого тела, кроме самой шапки.
for (const width of [1280, 375]) test(`шапка окна клиента закрывает прокручиваемое содержимое (${width}px)`, async ({ page }) => {
  await page.setViewportSize({ width, height: 720 });
  await login(page);
  await expect(page.getByLabel("Протокол: AWG").first()).toBeVisible();
  if (width < 640) await page.getByText("alice", { exact: true }).click();
  else await page.getByRole("button", { name: "alice", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("tab", { name: "AWG" })).toBeVisible();
  const body = dialog.locator('[data-slot="dialog-body"]');
  expect(await body.evaluate((el) => el.scrollHeight > el.clientHeight + 200)).toBe(true);
  await body.evaluate((el) => (el.scrollTop = 200));
  // Окно открывается с zoom-in: замер в середине анимации даёт сдвинутые
  // координаты, поэтому ждём, пока положение шапки перестанет меняться.
  const header = dialog.locator('[data-slot="dialog-header"]');
  let last = "";
  await expect.poll(async () => {
    const box = JSON.stringify(await header.boundingBox());
    const same = box === last;
    last = box;
    return same;
  }).toBe(true);

  const leaks = await page.evaluate(() => {
    const content = document.querySelector('[data-slot="dialog-content"]')!;
    const body = content.querySelector('[data-slot="dialog-body"]')!;
    const header = body.querySelector('[data-slot="dialog-header"]')!;
    const c = content.getBoundingClientRect();
    const h = header.getBoundingClientRect();
    const out: string[] = [];
    for (let y = c.top + 1; y < h.bottom; y += 3) {
      for (const x of [c.left + 4, c.left + 12, c.left + c.width / 2, c.right - 30]) {
        const el = document.elementFromPoint(x, y);
        // Скругление угла окна отдаёт точку подложке — это не содержимое.
        if (!el || !body.contains(el) || header.contains(el)) continue;
        out.push(`${Math.round(x)},${Math.round(y)}: ${el.tagName}.${el.className}`.slice(0, 120));
      }
    }
    return out;
  });
  expect(leaks).toEqual([]);

  // Крестик остаётся над шапкой: в его центре — он сам.
  const close = await page.evaluate(() => {
    const btn = document.querySelector('[data-slot="dialog-close"]')!;
    const r = btn.getBoundingClientRect();
    return btn.contains(document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2));
  });
  expect(close).toBe(true);
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
for (const width of [1280, 375]) test(`уведомление видно сверху справа, а не уезжает под край (${width}px)`, async ({ page }) => {
  await page.setViewportSize({ width, height: 900 });
  await login(page);

  // Тост вызывается сохранением настроек почты: это действие есть на любой
  // ширине, а меню карточки клиента на телефоне устроено иначе.
  await page.getByRole("button", { name: "Ещё" }).click();
  await page.getByRole("menuitem", { name: "Уведомления" }).click();
  const dialog = page.getByRole("dialog", { name: "Уведомления" });
  await dialog.getByLabel("Сервер SMTP").fill("smtp.example.org");
  await dialog.getByLabel("Логин").fill("vpn@example.org");
  await dialog.getByLabel("Пароль").fill("toast-check-password");
  await dialog.getByLabel("Куда присылать").fill("owner@example.org");
  await dialog.getByRole("button", { name: "Сохранить" }).click();

  const toast = page.locator("[data-sonner-toast]").first();
  await expect(toast).toBeAttached();
  await expect
    .poll(async () => {
      const box = await toast.boundingBox();
      const vp = page.viewportSize()!;
      return Boolean(box && box.y >= 0 && box.y + box.height <= vp.height && box.x >= 0 && box.x + box.width <= vp.width);
    })
    .toBe(true);
  // Сверху справа (amnezia-vpn-server-1f31): верх тоста в верхней трети
  // экрана, правый край у правого края.
  const box = (await toast.boundingBox())!;
  const vp = page.viewportSize()!;
  expect(box.y).toBeLessThan(vp.height / 3);
  expect(vp.width - (box.x + box.width)).toBeLessThan(40);
  await page.screenshot({ path: `test-results/toast-${width}.png` });
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

// Письма не уходят — панель говорит об этом над списком клиентов, а не
// только в окне (amnezia-vpn-server-pz2r). Отказ службы писем в e2e не
// устроить, поэтому ответ о состоянии подменяется.
for (const width of [1280, 375]) {
  test(`полоса «письма не уходят» видна и ведёт в настройки (${width}px)`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.route("**/api/mail", async (route) => {
      if (route.request().method() !== "GET") return route.continue();
      await route.fulfill({
        json: {
          ok: true,
          configured: true,
          host: "smtp.example.org",
          port: 587,
          username: "vpn@example.org",
          recipient: "owner@example.org",
          password_set: true,
          verified: true,
          test: { state: "ok", at_utc: "2026-09-14T01:00:00Z" },
          channel: "failing",
          last_success_at_utc: "2026-09-13T20:00:00Z",
          last_failure: {
            subject: "Туннель не работает 5 минут",
            summary: "Почтовый сервер не принял логин или пароль",
            error: "mailer: login as vpn@example.org: 535 5.7.8 Error: authentication failed",
            at_utc: "2026-09-14T02:00:00Z",
          },
        },
      });
    });
    await login(page);
    const alert = page.getByRole("alert").filter({ hasText: "Письма о сбоях не уходят" });
    await expect(alert).toBeVisible();
    const box = await alert.boundingBox();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(width);
    await page.screenshot({ path: `test-results/mail-banner-${width}.png` });
    await alert.getByRole("button", { name: "Открыть настройки" }).click();
    const notifications = page.getByRole("dialog", { name: "Уведомления" });
    await expect(notifications).toBeVisible();
    await expect(notifications.getByText("Почтовый сервер не принял логин или пароль")).toBeVisible();
    await page.waitForTimeout(400); // снимок после анимации открытия, а не посреди неё
    await page.screenshot({ path: `test-results/mail-dialog-failure-${width}.png` });
  });
}

// Пока меню «Ещё» открыто, на кнопке крестик, и он же меню закрывает
// (amnezia-vpn-server-3vec).
test("кнопка меню показывает крестик, пока меню открыто", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await login(page);
  // По атрибуту, а не по роли: пока меню открыто, Radix прячет остальную
  // страницу от дерева доступности, и кнопка по роли не находится.
  const trigger = page.locator('button[aria-label="Ещё"]');
  await expect(trigger.getByTestId("menu-more-icon")).toBeVisible();
  await trigger.click();
  await expect(page.getByRole("menu")).toBeVisible();
  await expect(trigger.getByTestId("menu-close-icon")).toBeVisible();
  await expect(trigger.getByTestId("menu-more-icon")).toHaveCount(0);
  await page.screenshot({ path: "test-results/menu-open.png" });
  // force: открытое меню Radix снимает pointer-events со страницы, и нажатие
  // по крестику приходит как нажатие мимо меню — меню закрывается. Именно
  // так это и происходит у человека; Playwright без force ждёт «кликабельности».
  await trigger.click({ force: true });
  await expect(page.getByRole("menu")).toHaveCount(0);
  await expect(trigger.getByTestId("menu-more-icon")).toBeVisible();
});

// Уведомления выключаются в окне, с подтверждением (amnezia-vpn-server-sjkk).
test("уведомления можно выключить", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await login(page);
  await page.getByRole("button", { name: "Ещё" }).click();
  await page.getByRole("menuitem", { name: "Уведомления" }).click();
  const dialog = page.getByRole("dialog", { name: "Уведомления" });
  await dialog.getByLabel("Сервер SMTP").fill("smtp.example.org");
  await dialog.getByLabel("Логин").fill("vpn@example.org");
  await dialog.getByLabel("Пароль").fill("disable-check-password");
  await dialog.getByLabel("Куда присылать").fill("owner@example.org");
  await dialog.getByRole("button", { name: "Сохранить" }).click();
  await expect(dialog.getByRole("button", { name: "Выключить уведомления" })).toBeVisible();
  await page.waitForTimeout(400);
  await page.screenshot({ path: "test-results/mail-disable-button.png" });

  await dialog.getByRole("button", { name: "Выключить уведомления" }).click();
  const confirm = page.getByRole("alertdialog", { name: "Выключить уведомления?" });
  await expect(confirm).toBeVisible();
  await page.waitForTimeout(400);
  await page.screenshot({ path: "test-results/mail-disable-confirm.png" });
  await confirm.getByRole("button", { name: "Выключить" }).click();

  await expect(dialog.getByLabel("Сервер SMTP")).toHaveValue("");
  await expect(dialog.getByRole("button", { name: "Выключить уведомления" })).toHaveCount(0);
  await page.reload();
  await page.getByRole("button", { name: "Ещё" }).click();
  await page.getByRole("menuitem", { name: "Уведомления" }).click();
  await expect(page.getByRole("dialog", { name: "Уведомления" }).getByLabel("Сервер SMTP")).toHaveValue("");
});

// amnezia-vpn-server-14y0.5: в окне клиента рядом с AWG — ссылки VLESS по
// маршрутам. Копирование кладёт в буфер ровно ту ссылку, что зашита в QR, а
// QR приходит с сервера картинкой. Обе ширины: на телефоне раздел открывают
// чаще всего.
for (const width of [1280, 375]) test(`окно клиента выдаёт ссылки и QR VLESS (${width}px)`, async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.setViewportSize({ width, height: 900 });
  await login(page);
  // На телефоне окно открывает нажатие на саму карточку, на широком экране —
  // кнопка с именем.
  // Строка клиента называет протокол последней активности; alice ещё ни
  // разу не была на связи — AWG (amnezia-vpn-server-14y0.8).
  await expect(page.getByLabel("Протокол: AWG").first()).toBeVisible();
  if (width < 640) await page.getByText("alice", { exact: true }).click();
  else await page.getByRole("button", { name: "alice", exact: true }).click();

  // Окно открывается на вкладке AWG; ссылки VLESS — на своей вкладке, и
  // выбранная вкладка видна (amnezia-vpn-server-14y0.8).
  const awgTab = page.getByRole("tab", { name: "AWG" });
  const vlessTab = page.getByRole("tab", { name: "VLESS" });
  await expect(awgTab).toHaveAttribute("aria-selected", "true");
  await expect(page.getByText("Напрямую", { exact: true })).toHaveCount(0);
  await vlessTab.click();
  await expect(vlessTab).toHaveAttribute("aria-selected", "true");
  await expect(page.getByText("Напрямую", { exact: true })).toBeVisible();
  // Оговорки про вход больше нет: трафик через вход учитывается
  // (amnezia-vpn-server-14y0.12).
  await expect(page.getByText(/Подключения через вход/)).toHaveCount(0);
  // Маршрут через узел-вход называется «Каскад» (amnezia-vpn-server-14y0.17).
  await expect(page.getByText("Каскад", { exact: true })).toBeVisible();
  // amnezia-vpn-server-14y0.9: у VLESS свой график, «Удалить» — внизу окна,
  // и окно не прокручивается вбок ни на одной ширине.
  await expect(page.getByRole("tabpanel").getByText(/^Скорость/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Удалить" })).toBeVisible();
  const body = page.locator('[data-slot="dialog-body"]').first();
  expect(await body.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);

  await page.getByRole("button", { name: "Скопировать ссылку: Каскад", exact: true }).click();
  await expect(page.getByText("Ссылка скопирована")).toBeVisible();
  const link = await page.evaluate(() => navigator.clipboard.readText());
  expect(link).toMatch(/^vless:\/\/[0-9a-f-]{36}@entry\.example\.com:443\?type=xhttp&path=%2Fentry&/);
  // Подпись в приложении — «VLESS <имя>» (решение владельца 28.10.2026).
  expect(link.endsWith("#VLESS%20alice")).toBe(true);

  await page.getByRole("button", { name: "QR: Напрямую" }).click();
  const img = page.getByRole("img", { name: "QR-код VLESS клиента alice, маршрут «Напрямую»" });
  await expect(img).toBeVisible();
  await expect.poll(async () => img.evaluate((el: HTMLImageElement) => el.naturalWidth)).toBeGreaterThan(0);
  // Ширина: окно QR не выезжает за экран.
  const box = await img.boundingBox();
  expect(box && box.x >= 0 && box.x + box.width <= width).toBe(true);
});

// amnezia-vpn-server-14y0.13: пункт «QR-код» в меню строки показывал только
// AWG. Теперь в окне выбор протокола и, у VLESS, маршрута; открывается на
// протоколе последней активности (alice ещё не была на связи — AWG).
for (const width of [1280, 375]) test(`окно QR из меню строки: AWG и VLESS (${width}px)`, async ({ page }) => {
  await page.setViewportSize({ width, height: 900 });
  await login(page);
  // Меню строки есть только на широком экране; на телефоне окно QR
  // открывает кнопка «QR» во вкладке AWG окна клиента — тоже на AWG.
  if (width < 640) {
    await page.getByText("alice", { exact: true }).click();
    await page.getByRole("tabpanel").getByRole("button", { name: "QR", exact: true }).click();
  } else {
    await page.getByRole("button", { name: "Действия для alice" }).click();
    await page.getByRole("menuitem", { name: "QR-код" }).click();
  }
  const dialog = page.getByRole("dialog", { name: "QR-код: alice" });
  await expect(dialog.getByRole("button", { name: "AWG" })).toHaveAttribute("aria-pressed", "true");
  const awg = dialog.getByRole("img", { name: "QR-код AmneziaWG клиента alice" });
  await expect.poll(async () => awg.evaluate((el: HTMLImageElement) => el.naturalWidth)).toBeGreaterThan(0);

  await dialog.getByRole("button", { name: "VLESS" }).click();
  await expect(dialog.getByRole("group", { name: "Маршрут" })).toBeVisible();
  const direct = dialog.getByRole("img", { name: "QR-код VLESS клиента alice, маршрут «Напрямую»" });
  await expect.poll(async () => direct.evaluate((el: HTMLImageElement) => el.naturalWidth)).toBeGreaterThan(0);
  // Вкладки и маршрут — одна строка: протокол слева, маршрут справа, и
  // на 375px вкладки не ломаются (amnezia-vpn-server-14y0.17).
  const protoBox = await dialog.getByRole("group", { name: "Протокол" }).boundingBox();
  const routeBox = await dialog.getByRole("group", { name: "Маршрут" }).boundingBox();
  const vlessBox = await dialog.getByRole("button", { name: "VLESS" }).boundingBox();
  const awgBox = await dialog.getByRole("button", { name: "AWG" }).boundingBox();
  expect(protoBox && routeBox && routeBox.x > protoBox.x + protoBox.width).toBe(true);
  expect(awgBox && vlessBox && awgBox.y === vlessBox.y).toBe(true);
  await expect(dialog.getByText(/Отсканируйте|Подходит/)).toHaveCount(0);
  // Клик мышью не оставляет фокус на кнопках окна: нажатие клавиши после
  // него не рисует контур (amnezia-vpn-server-14y0.17).
  await page.keyboard.press("Shift");
  await expect(dialog.locator("button:focus-visible")).toHaveCount(0);
  await dialog.getByRole("button", { name: "Каскад", exact: true }).click();
  // Выбранный маршрут виден и под курсором: фон только у него.
  const routeBg = async (name: string) =>
    dialog.getByRole("button", { name, exact: true }).evaluate((el) => getComputedStyle(el).backgroundColor);
  await expect(dialog.getByRole("button", { name: "Каскад", exact: true })).toHaveAttribute("aria-pressed", "true");
  await dialog.getByRole("button", { name: "Напрямую", exact: true }).hover();
  // Цвет меняется с переходом (transition-all) — ждём, пока он закончится.
  await expect.poll(() => routeBg("Напрямую")).toBe("rgba(0, 0, 0, 0)");
  await expect.poll(() => routeBg("Каскад")).not.toBe("rgba(0, 0, 0, 0)");
  const viaEntry = dialog.getByRole("img", { name: "QR-код VLESS клиента alice, маршрут «Каскад»" });
  await expect(viaEntry).toHaveAttribute("src", /\/vless\/qr\?entry=entry$/);
  await expect.poll(async () => viaEntry.evaluate((el: HTMLImageElement) => el.naturalWidth)).toBeGreaterThan(0);
  const box = await viaEntry.boundingBox();
  expect(box && box.x >= 0 && box.x + box.width <= width).toBe(true);
});

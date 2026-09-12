import { expect, test, type Page } from "@playwright/test";

// Полоса о новом выпуске против настоящей панели (amnezia-vpn-server-tjoq,
// amnezia-vpn-server-8bt5). Снимок выпуска новее установленного кладёт
// фикстура: без него полосу можно было бы проверить только дождавшись
// настоящего выпуска.
const user = "e2e";
const password = "e2e-password-correct-horse";

// Вход без разбора итога. Окно с итогом обновления модальное и перекрывает
// панель до подтверждения — это и задумано, поэтому помощник его закрывает,
// как закрыл бы человек. Сам итог проверяется отдельным тестом, который
// входит без этого помощника.
async function signIn(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Имя пользователя").fill(user);
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти" }).click();
}

async function login(page: Page) {
  await signIn(page);
  // Итог приходит отдельным запросом уже после входа, и окно всплывает не
  // мгновенно. Вопрос «окно есть?», заданный сразу после клика, отвечал «нет»,
  // окно появлялось следом и перекрывало панель до конца теста. Дожидаемся
  // тишины в сети и только потом спрашиваем (amnezia-vpn-server-y9wx).
  await page.waitForLoadState("networkidle");
  const acknowledge = page.getByRole("button", { name: "Понятно" });
  if (await acknowledge.isVisible().catch(() => false)) {
    await acknowledge.click();
  }
  await expect(page.getByRole("button", { name: "Добавить клиента" })).toBeVisible();
}

// Итог прошлого обновления встречает человека сам: обновление перезапускает
// саму панель, и если итог не показать — он не узнает ничего.
test("итог прошлого обновления показывается сам", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await signIn(page);
  await expect(page.getByRole("heading", { name: "Обновление установлено" })).toBeVisible();
  // Версию панель называет сама, а не пересказывает строку хоста
  // (amnezia-vpn-server-jdkq).
  await expect(page.getByText("Панель обновлена до версии 99.9.9")).toBeVisible();
  // Закрыли — и он не возвращается: сервер помнит, какой итог показали.
  await page.getByRole("button", { name: "Понятно" }).click();
  await page.reload();
  await expect(page.getByRole("button", { name: "Добавить клиента" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Обновление установлено" })).toHaveCount(0);
  // И полоса под ним никуда не делась: итог её перекрывал, а не отменял.
  await expect(page.getByText("Вышла версия 99.9.9")).toBeVisible();
});

test("полоса называет вышедшую версию", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page);
  await expect(page.getByText("Вышла версия 99.9.9")).toBeVisible();
});

test("подробности показывают изменения и предупреждают о перерыве", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page);
  await page.getByRole("button", { name: "Показать подробности" }).click();
  // Пункт, перенесённый в теле выпуска по ширине, собирается обратно в один
  // буллит: прежде каждая физическая строка становилась своим абзацем, и
  // фраза разрывалась посреди себя (amnezia-vpn-server-u1fv).
  const items = page.getByRole("dialog").getByRole("listitem");
  await expect(items).toHaveCount(2);
  await expect(items.first()).toHaveText(
    "Первое изменение заняло столько слов, что тело выпуска перенесло его на вторую строку с отступом.",
  );
  // И то, что вышло между установленной версией и свежей, тоже: человек
  // решает по тому, что изменится у него, а не по последней записи.
  await expect(items.nth(1)).toHaveText("Второе изменение");
  await expect(page.getByText(/Клиенты будут без связи/)).toBeVisible();
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
  // Система — то, что даёт deployment.json фикстуры; ряд не пустует.
  await expect(page.getByText("ubuntu 24.04 (noble)")).toBeVisible();
  // Номер схемы SQLite оператору ничего не говорит и убран из окна целиком
  // (amnezia-vpn-server-4yo4): разработчику он доступен через CLI.
  await expect(page.getByText("Схема базы")).toHaveCount(0);
  // Панель, запущенная без развёртывания, про хост не знает — и обязана
  // сказать именно это, а не оставить пустоту.
  await expect(page.getByText("Неизвестно").first()).toBeVisible();
});

// Состояние служб (amnezia-vpn-server-eq82): отказ, который чинится сам,
// невидим, пока о нём негде прочитать.
test("окно «Состояние служб» показывает, что сервер сам себя чинил", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page);
  await page.getByRole("button", { name: "Ещё" }).click();
  await page.getByRole("menuitem", { name: "Состояние служб" }).click();

  await expect(page.getByRole("heading", { name: "Состояние служб" })).toBeVisible();
  await expect(page.getByText("Резолвер в туннеле")).toBeVisible();
  await expect(page.getByText("Туннель")).toBeVisible();
  // Сейчас всё работает — и при этом видно, что резолвер перезапускали.
  await expect(page.getByText(/Перезапускался/)).toBeVisible();
  await expect(page.getByText(/не отвечает на 10\.8\.0\.1/)).toBeVisible();
});

// Журнал (amnezia-vpn-server-gqep): защита от «я такого не делал».
test("журнал показывает вход и изменения", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page);
  // Само изменение: включаем и выключаем клиента, чтобы записи было чему
  // появиться.
  await page.getByRole("button", { name: /Действия для/ }).first().click();
  await page.getByRole("menuitem", { name: /Отключить|Включить/ }).click();
  // Ждём подтверждения от сервера, а не своего клика: всплывающее сообщение
  // появляется только после ответа на PATCH. Без него журнал спрашивался
  // раньше, чем запись в него попадала, и на медленной машине проверка падала
  // (amnezia-vpn-server-y9wx).
  await expect(page.getByText(/^Клиент (отключён|включён)$/)).toBeVisible();

  await page.getByRole("button", { name: "Ещё" }).click();
  await page.getByRole("menuitem", { name: "Журнал" }).click();

  await expect(page.getByRole("heading", { name: "Журнал" })).toBeVisible();
  // Подписи журнала пишутся с большой буквы (amnezia-vpn-server-4cnf).
  await expect(page.getByText(/^Клиент/).first()).toBeVisible();
  await expect(page.getByText("Вход").first()).toBeVisible();
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

// Крестик пропадал из ВСЕХ окон разом: закреплённая шапка получила z-10 и
// рисовалась поверх него. В разметке он оставался, поэтому тесты на наличие
// молчали — видно это только глазами или проверкой видимости
// (amnezia-vpn-server-jy87).
test("крестик виден в окнах со шапкой", async ({ page }) => {
  await login(page);

  for (const item of ["Состояние служб", "Журнал"]) {
    await page.getByRole("button", { name: "Ещё" }).click();
    await page.getByRole("menuitem", { name: item }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    // Не toBeVisible: она смотрит только на CSS и молчит, когда элемент
    // ЗАКРЫТ другим. Именно так регрессия и прошла мимо проверок — крестик
    // оставался «видимым», а поверх него лежала шапка. Проверяем то, что
    // делает человек: нажимаем и ждём, что окно закроется.
    await dialog.locator("[data-slot=dialog-close]").click({ timeout: 5000 });
    await expect(dialog).toHaveCount(0);
  }
});

// Пересъёмка скриншотов README (amnezia-vpn-server-14y0.15).
//
//   cd app/panel/web && npm ci && npm run screenshots
//
// Пишет screenshots/main.png, client.png, qr.png и mobile.png в корне
// репозитория. Прошлая съёмка (amnezia-vpn-server-y2tj) делалась скриптом,
// который в репозиторий не попал, и к выпуску 3.0.0 его пришлось писать
// заново; теперь он здесь, и следующий раз — одна команда.
//
// Панель поднимается так же, как в web/e2e/run-panel.sh: из app/panel с
// путями AMNEZIA_*_PATH во временный каталог, без Docker и без root. Всё,
// что на живом сервере пишут другие (status.json и журнал скорости — awg,
// счётчики — Xray), здесь подкладывает скрипт:
//
//   - status.json и speed.log — как их писал бы контейнер awg;
//   - API Xray — поддельный, на Node http2 (h2c): панель опрашивает
//     StatsService раз в пять секунд, и только ответ на этот опрос делает
//     «VLESS работает» в шапке. Настоящий Xray ради картинки не нужен;
//   - загрузка процессора, памяти и диска — подменой ответа /api/stats/host:
//     панель читает их из /proc хоста, которого на macOS нет.
//
// Данные вымышленные: адреса из документационных диапазонов (RFC 5737),
// домены — example.com, ключи Reality и узла-входа — случайные.
//
// pngquant и oxipng (brew install pngquant oxipng) необязательны: без них
// картинки остаются такими, как их снял браузер, только крупнее.

import { spawn, spawnSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import fs from "node:fs";
import http2 from "node:http2";
import os from "node:os";
import path from "node:path";
import { DatabaseSync } from "node:sqlite";
import { fileURLToPath } from "node:url";
import { chromium } from "@playwright/test";

const here = path.dirname(fileURLToPath(import.meta.url));
const panelDir = path.resolve(here, "../..");
const outDir = path.resolve(panelDir, "../../screenshots");
const port = Number(process.env.AMNEZIA_SHOTS_PORT ?? 18790);
const baseURL = `http://127.0.0.1:${port}`;
const user = "admin";
const password = "readme-screenshots-password";

const dir = fs.mkdtempSync(path.join(os.tmpdir(), "panel-shots-"));
const env = {
  ...process.env,
  AMNEZIA_DB_PATH: path.join(dir, "amnezia.sqlite"),
  AMNEZIA_CONFIG_PATH: path.join(dir, "awg0.conf"),
  AMNEZIA_STATUS_PATH: path.join(dir, "status.json"),
  AMNEZIA_BACKUPS_PATH: path.join(dir, "backups"),
  AMNEZIA_XRAY_CONFIG_PATH: path.join(dir, "xray.json"),
  // API Xray — по TCP, а не в unix-сокете, как на сервере: брошенный сокет
  // панель при запуске удаляет, если на нём нет flock Xray, а взять flock
  // из Node нечем.
  AMNEZIA_XRAY_API_ADDR: `127.0.0.1:${port + 1}`,
  AMNEZIA_SECURE_COOKIES: "",
};
fs.mkdirSync(env.AMNEZIA_BACKUPS_PATH);
const bin = path.join(dir, "panel");

function run(args, input) {
  const r = spawnSync(bin, args, { cwd: panelDir, env, input, encoding: "utf8" });
  if (r.status !== 0) {
    throw new Error(`panel ${args.join(" ")}: ${r.stderr || r.stdout}`);
  }
  return r.stdout;
}

// Клиенты: обычные короткие имена (на телефоне рядом с именем значок
// протокола, и длинное обрезается), разный трафик, часть ходит по VLESS,
// один на паузе. awg и vless — когда клиент последний раз был на связи по
// протоколу (секунд назад), down/up — накопленное, в гигабайтах. Карточка
// показывает протокол, по которому клиент был на связи последним.
const people = [
  { name: "Аня", description: "Pixel 8, основной телефон", awg: 21, down: 9.1, up: 1.2, vless: 3 * 3600, vDown: 1.4, vUp: 0.2 },
  { name: "Илья", description: "Ноутбук", vless: 0, vDown: 18.6, vUp: 2.7, active: true },
  { name: "Роутер", description: "Keenetic в прихожей, через него весь дом", awg: 64, down: 38.1, up: 3.0 },
  { name: "Папин ПК", awg: 2 * 3600 + 300, down: 12.7, up: 2.3 },
  { name: "Маша", description: "iPad", vless: 27 * 60, vDown: 24.2, vUp: 1.6 },
  { name: "Мама", awg: 3 * 3600, down: 2.1, up: 0.6, vless: 130, vDown: 0.9, vUp: 0.1 },
  { name: "Телевизор", awg: 26 * 3600, down: 41.3, up: 0.9 },
  { name: "Офис", description: "Рабочий ноутбук", disabled: true },
];
// Окно клиента и QR снимаются с этого клиента: у него оба протокола.
const featured = "Аня";

const GB = 1024 ** 3;
const now0 = Math.floor(Date.now() / 1000);
const xrayStarted = now0 - 3 * 24 * 3600;

// Скорость — детерминированный «шум» с всплеском посередине окна: ровную
// линию график нарисует и по ошибке, а случайность от запуска к запуску
// меняла бы картинку без причины.
function noise(seed, k) {
  let x = (seed * 374761393 + k * 668265263) | 0;
  x = Math.imul(x ^ (x >>> 13), 1274126177);
  return ((x ^ (x >>> 16)) >>> 0) / 4294967296;
}
function rate(seed, k) {
  // Всплеск — в середине десятиминутного окна графика.
  const pos = k - Math.floor((now0 - 600) / 5);
  const burst = pos > 50 && pos < 74 ? 2.5 + 2 * noise(seed + 7, k) : 1;
  const base = 0.6 + 1.6 * noise(seed, k) + 1.2 * noise(seed + 3, k >> 2);
  return Math.round(base * burst * 1_000_000); // байт/с к клиенту
}
// counter — накопленные байты к клиенту к моменту t: сумма rate по
// пятисекундным шагам начиная с часа до запуска. Одна и та же функция
// пишет журнал и отвечает за Xray, поэтому живые замеры панели продолжают
// подложенный журнал без скачка.
function counter(seed, t) {
  const from = Math.floor((now0 - 3600) / 5);
  const to = Math.floor(t / 5);
  let sum = 0;
  for (let k = from; k < to; k++) sum += rate(seed, k) * 5;
  return sum + rate(seed, to) * (t - to * 5);
}

function writeSpeedLog(file, peers, handshakeAge) {
  const lines = ["#speed v2"];
  for (let t = now0 - 600; t <= now0; t += 5) {
    let line = String(t);
    for (const p of peers) {
      const moved = p.idle ? 0 : counter(p.seed, t);
      const down = p.baseDown + moved;
      const up = p.baseUp + Math.round(moved / 12);
      line += ` ${p.key}:${up}:${down}:${handshakeAge(p, t)}`;
    }
    lines.push(line);
  }
  fs.writeFileSync(file, lines.join("\n") + "\n", { mode: 0o600 });
}

// --- Поддельный API Xray -------------------------------------------------

function varint(n) {
  const out = [];
  let v = BigInt(n);
  while (v >= 0x80n) {
    out.push(Number((v & 0x7fn) | 0x80n));
    v >>= 7n;
  }
  out.push(Number(v));
  return Buffer.from(out);
}
function bytesField(field, data) {
  return Buffer.concat([varint((field << 3) | 2), varint(data.length), data]);
}
function varintField(field, n) {
  return Buffer.concat([varint(field << 3), varint(n)]);
}

function startFakeXray(listenPort, vlessPeers) {
  const server = http2.createServer();
  server.on("stream", (stream, headers) => {
    const p = headers[":path"];
    stream.resume();
    stream.on("end", () => {
      const now = Math.floor(Date.now() / 1000);
      let msg = Buffer.alloc(0);
      if (p.endsWith("/GetSysStats")) {
        msg = varintField(10, now - xrayStarted);
      } else if (p.endsWith("/QueryStats")) {
        const stats = [];
        for (const v of vlessPeers) {
          const moved = v.active ? counter(v.seed, now) : 0;
          for (const [dir, n] of [
            ["downlink", v.counterDown + moved],
            ["uplink", v.counterUp + Math.round(moved / 12)],
          ]) {
            const name = `user>>>c${v.id}>>>traffic>>>${dir}`;
            stats.push(bytesField(1, Buffer.concat([bytesField(1, Buffer.from(name)), varintField(2, n)])));
          }
        }
        msg = Buffer.concat(stats);
      }
      // Остальное (AlterInbound при синхронизации пользователей) — пустой
      // успешный ответ: пользователь «добавлен».
      const frame = Buffer.alloc(5);
      frame.writeUInt32BE(msg.length, 1);
      stream.respond({ ":status": 200, "content-type": "application/grpc" }, { waitForTrailers: true });
      stream.on("wantTrailers", () => stream.sendTrailers({ "grpc-status": "0" }));
      stream.end(msg.length ? Buffer.concat([frame, msg]) : undefined);
    });
  });
  return new Promise((resolve) => server.listen(listenPort, "127.0.0.1", () => resolve(server)));
}

// --- Подготовка данных ---------------------------------------------------

function prepare() {
  const build = spawnSync("go", ["build", "-o", bin, "."], { cwd: panelDir, env, stdio: "inherit" });
  if (build.status !== 0) throw new Error("go build failed");

  run(["auth", "add-user", user, "--password-stdin"], `${password}\n`);
  run(["server", "init", "10.8.0.1/24", "51820", "--endpoint", "vpn.example.com:51820"]);
  for (const p of people) run(["client", "add", p.name]);
  run(["xray", "init", "--server-name", "vpn.example.com"]);
  // Узел-вход — чтобы у QR VLESS было из чего выбирать маршрут.
  run([
    "xray", "entry", "set", "relay",
    "--host", "relay.example.com", "--sni", "relay.example.com",
    "--public-key", randomBytes(32).toString("base64url"),
    "--short-id", randomBytes(4).toString("hex"), "--path", "/relay",
  ]);

  const db = new DatabaseSync(env.AMNEZIA_DB_PATH);
  const rows = db.prepare("SELECT id, name, public_key, address FROM clients ORDER BY id").all();
  const server = db.prepare("SELECT public_key FROM server").get();
  const byName = new Map(rows.map((r) => [r.name, r]));
  const setDesc = db.prepare("UPDATE clients SET description = ? WHERE id = ?");
  const putVless = db.prepare(
    `INSERT INTO vless_traffic (client_id, up_bytes, down_bytes, counter_up, counter_down, counter_since_unix, last_active_utc)
     VALUES (?, ?, ?, ?, ?, ?, ?)`,
  );
  const peers = [];
  const vlessPeers = [];
  people.forEach((p, i) => {
    const row = byName.get(p.name);
    p.id = row.id;
    if (p.description) setDesc.run(p.description, row.id);
    if (p.disabled) run(["client", "disable", String(row.id)]);
    if (p.vless !== undefined) {
      // Счётчики Xray с его запуска и накопленное панелью — как будто
      // панель опрашивала его всё это время. Активные растут дальше уже в
      // ответах поддельного API.
      const v = {
        id: row.id,
        seed: 100 + i,
        active: !!p.active,
        counterDown: Math.round(p.vDown * GB * 0.3),
        counterUp: Math.round(p.vUp * GB * 0.3),
      };
      const at = new Date((now0 - p.vless) * 1000).toISOString();
      putVless.run(row.id, Math.round(p.vUp * GB), Math.round(p.vDown * GB),
        v.counterUp + (v.active ? Math.round(counter(v.seed, now0) / 12) : 0),
        v.counterDown + (v.active ? counter(v.seed, now0) : 0),
        xrayStarted, at);
      vlessPeers.push(v);
    }
    if (!p.disabled) {
      peers.push({ p, row, seed: i + 1 });
    }
  });
  db.close();

  // status.json, как его пишет контейнер awg: рукопожатия и трафик пиров.
  const status = {
    schema: "v1",
    generated_at_utc: new Date(now0 * 1000).toISOString().replace(/\.\d+Z$/, "Z"),
    interface: {
      iface: "awg0", has_interface: true, public_key: server.public_key, listen_port: 51820, fwmark: "off",
      awg_params: { jc: 4, jmin: 40, jmax: 70, s1: 0, s2: 0, s3: 0, s4: 0 },
    },
    peers: peers.map(({ p, row }, i) => ({
      public_key: row.public_key,
      endpoint: p.awg === undefined ? "(none)" : `198.51.100.${20 + i}:${41000 + i * 137}`,
      allowed_ips: [row.address],
      last_handshake_utc: p.awg === undefined ? null : new Date((now0 - p.awg) * 1000).toISOString(),
      rx_bytes: Math.round((p.up ?? 0) * GB),
      tx_bytes: Math.round((p.down ?? 0) * GB),
      persistent_keepalive: "off",
    })),
  };
  fs.writeFileSync(env.AMNEZIA_STATUS_PATH, JSON.stringify(status), { mode: 0o600 });

  // Журнал скорости AWG — последние десять минут клиентов, что на связи.
  const awgOnline = peers.filter(({ p }) => p.awg !== undefined && p.awg < 180);
  writeSpeedLog(
    path.join(dir, "speed.log"),
    awgOnline.map(({ p, row, seed }) => ({
      key: row.public_key.slice(0, 12), seed, p,
      baseDown: Math.round(p.down * GB) - counter(seed, now0),
      baseUp: Math.round(p.up * GB) - Math.round(counter(seed, now0) / 12),
    })),
    // Рукопожатие раз в две минуты, последнее — peer.p.awg секунд назад.
    (peer, t) => (((peer.p.awg - (now0 - t)) % 120) + 120) % 120,
  );
  // Журнал скорости VLESS лежит у панели в каталоге данных и ключуется
  // номером клиента. Пишем всех с UUID, как пишет сама панель.
  const vlessIds = new Set(JSON.parse(run(["xray", "users-json"])).map((u) => u.client_id));
  writeSpeedLog(
    path.join(dir, "vless-speed.log"),
    rows.filter((r) => vlessIds.has(r.id)).map((r) => {
      const v = vlessPeers.find((x) => x.id === r.id);
      return { key: `c${r.id}`, seed: v?.seed ?? 0, idle: !v?.active,
        baseDown: v?.counterDown ?? 0, baseUp: v?.counterUp ?? 0 };
    }),
    () => "-",
  );
  return vlessPeers;
}

// --- Съёмка --------------------------------------------------------------

async function waitForPanel(child) {
  for (let i = 0; i < 100; i++) {
    if (child.exitCode !== null) throw new Error("panel exited");
    try {
      if ((await fetch(`${baseURL}/login`)).ok) return;
    } catch {
      // ещё не слушает
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error("panel did not start");
}

async function newPage(browser, viewport, scale) {
  const context = await browser.newContext({
    baseURL, viewport, deviceScaleFactor: scale, colorScheme: "light", locale: "ru-RU",
    timezoneId: "Europe/Berlin",
    isMobile: viewport.width < 500, hasTouch: viewport.width < 500,
  });
  await context.addInitScript(() => {
    try {
      localStorage.setItem("amnezia-theme", "light");
    } catch {
      // без хранилища тему задаст colorScheme
    }
  });
  // Загрузка хоста: на macOS /proc нет, и шапка показала бы прочерки.
  // Остальное (состояние AWG и VLESS) — настоящий ответ панели.
  await context.route("**/api/stats/host", async (route) => {
    const response = await route.fetch();
    const json = await response.json();
    Object.assign(json, {
      cpu_percent: 6, ram_percent: 41, disk_percent: 27,
      ram_used_bytes: 1.6 * GB, ram_total_bytes: 3.9 * GB,
      disk_used_bytes: Math.round(6.8 * GB), disk_total_bytes: 25 * GB,
    });
    await route.fulfill({ response, json });
  });
  const page = await context.newPage();
  await page.goto("/login");
  await page.getByLabel("Имя пользователя").fill(user);
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти" }).click();
  await page.getByTestId("client-grid").waitFor();
  // «VLESS работает» — после первого ответа Xray на опрос панели (раз в
  // пять секунд); шапка перечитывает состояние сама.
  await page.waitForFunction(async () => {
    const r = await fetch("/api/stats/host");
    return r.ok && (await r.json()).vless_state === "up";
  }, null, { polling: 1000, timeout: 30_000 });
  await page.reload();
  await page.getByTestId("client-grid").waitFor();
  await page.waitForTimeout(1500);
  return page;
}

function optimize(file) {
  const has = (cmd) => spawnSync("sh", ["-c", `command -v ${cmd}`]).status === 0;
  if (has("pngquant")) {
    spawnSync("pngquant", ["--force", "--skip-if-larger", "--quality=80-95", "--strip", "--output", file, file]);
  }
  if (has("oxipng")) {
    spawnSync("oxipng", ["-o", "4", "--strip", "safe", "-q", file]);
  }
}

async function shoot(browser) {
  // Главный экран: шапка и список, без пустого поля под ним.
  {
    const page = await newPage(browser, { width: 860, height: 760 }, 2);
    const grid = await page.getByTestId("client-grid").boundingBox();
    const file = path.join(outDir, "main.png");
    await page.screenshot({ path: file, clip: { x: 0, y: 0, width: 860, height: Math.ceil(grid.y + grid.height + 40) } });
    optimize(file);
    await page.context().close();
  }
  // Окно клиента и его QR поверх.
  {
    const page = await newPage(browser, { width: 1280, height: 800 }, 2);
    await page.getByRole("button", { name: featured, exact: true }).click();
    const dialog = page.getByRole("dialog");
    await dialog.waitFor();
    await page.waitForTimeout(1200);
    let file = path.join(outDir, "client.png");
    await page.screenshot({ path: file });
    optimize(file);

    await dialog.getByRole("button", { name: "QR", exact: true }).click();
    const qr = page.getByRole("dialog", { name: new RegExp(`QR`) });
    await qr.waitFor();
    // Переключатель протокола — вкладка или кнопка-сегмент: так скрипт
    // переживёт перестановку окна, не завязываясь на подписи маршрутов.
    await qr.getByRole("tab", { name: "VLESS", exact: true })
      .or(qr.getByRole("button", { name: "VLESS", exact: true })).first().click();
    await qr.locator("img").first().waitFor();
    await page.waitForTimeout(1200);
    file = path.join(outDir, "qr.png");
    await page.screenshot({ path: file });
    optimize(file);
    await page.context().close();
  }
  // Телефон: 390×844 в три точки на пиксель, как iPhone.
  {
    const page = await newPage(browser, { width: 390, height: 844 }, 3);
    const file = path.join(outDir, "mobile.png");
    await page.screenshot({ path: file });
    optimize(file);
    await page.context().close();
  }
}

async function main() {
  const vlessPeers = prepare();
  const xray = await startFakeXray(port + 1, vlessPeers);
  const child = spawn(bin, ["serve", "--addr", `127.0.0.1:${port}`], { cwd: panelDir, env, stdio: "inherit" });
  let browser;
  try {
    await waitForPanel(child);
    browser = await chromium.launch();
    await shoot(browser);
    console.log(`screenshots written to ${outDir}`);
  } finally {
    await browser?.close();
    child.kill();
    xray.close();
    if (!process.env.AMNEZIA_SHOTS_KEEP) fs.rmSync(dir, { recursive: true, force: true });
  }
}

await main();

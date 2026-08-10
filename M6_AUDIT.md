# M6 AUDIT — BASIC PANEL CRUD + CLIENT CARDS

Дата: 2026-08-10. Состояние: **аудит завершён, контракт выведен из источников. Реализация завершена (M6.1–M6.7), см. §12.**
Источники: `docs/TECHNICAL_SPEC_v2.0.md` (§6, §10, §11), `docs/ARCHITECTURE.md`, `docs/DEPLOYMENT.md`, `docs/SECURITY.md`, код panel/awg после M5, `M5_AUDIT.md`.

## 0. СОСТОЯНИЕ РЕПОЗИТОРИЯ ПОСЛЕ M5

`git status`: **M5 НЕ закоммичен** — в рабочем дереве модифицированы `app/awg/Dockerfile`, `app/awg/entrypoint.sh`, `app/panel/internal/cli/cli.go` (переход к `cmdServe`-стабу, поведение и текст ошибки сохранены); untracked: `cmd/awgstatus/`, `internal/status/`, `internal/cli/status*.go`, `test_m5*.sh`, `M5_AUDIT.md`, `CHANGELOG.md`. Последний коммит — `05024e2 M4.3/M4.4`. M6-аудит считает M5 baseline; CHANGELOG.md не трогался до отдельного указания.

## 1. M6 SCOPE (только из источников)

**ТЗ §10 (milestone)**: `M6  Basic panel CRUD + client cards`.

| Источник | Факт |
|---|---|
| ТЗ §6, таблица операций | Add, Delete, Enable/disable, Download config, QR — все **"Generate on demand"** |
| ТЗ §6 | Client card: **name, online/offline, last handshake, RX/TX, creation date**; **online = handshake ≤ 3 min** |
| ТЗ §6 | UI: server-side `html/template`, вертикальный стек карточек, минимальный JS, **без SPA/framework** |
| ТЗ §6 | Один бинарь `/app/panel` с подкомандами, включая `serve` |
| ТЗ (критерий M3.1, стр. 340) | `serve` "keeps exiting non-zero **until M6**" → M6 реализует `serve` |
| ТЗ стр. 391 | "No QR in M4 (**scheduled for M6**)" → QR ∈ M6 |
| DEPLOYMENT.md §Ports | "The panel HTTP port **will be exposed in a later milestone (M6)**, when the web UI lands" → `ports:` в compose ∈ M6 |
| M5_AUDIT.md §1 + cli/status.go | **reconciliation SQLite ↔ status.json = M6** (consumer-часть); панель читает status.json строго RO |
| ТЗ стр. 46 + §11 | **No inter-container HTTP/REST API** — исключение M0–M8. HTTP только браузер→panel |
| ТЗ §10 + §11 | Auth = **M7** (username/password, Argon2 или bcrypt, cookie-сессия); TOTP = **M9.5**; Backup UI = M8; роли/графики/ORM/Redis — вне M0–M8 |

**Чего в M6 НЕТ (явно или по исключениям)**: authentication/session enforcement (M7), TOTP (M9), межконтейнерный HTTP (запрещён всегда), изменения CLI/db/awgconf/status (только переиспользование), изменения awg-контейнера.

## 2. M6 CONTRACT (реконструкция)

### 2.1 Возможности (обязательные)

1. `panel serve` — реальный HTTP-сервер (server-side HTML, `html/template`).
2. Dashboard: вертикальный стек клиентских карточек и формы действий.
3. Карточка = **join SQLite `clients` × `status.json.peers` по `public_key`** (reconciliation):
   - name, creation date — из `clients` (`ClientsAll`/`ClientByID`);
   - online/offline, last handshake, RX/TX — из `status.Peer` (`last_handshake_utc`, `rx_bytes`, `tx_bytes`);
   - **online**: `last_handshake_utc != null && now − last_handshake_utc ≤ 3 min` (ТЗ §6);
   - клиент без записи peer → offline; peer без клиента → не показывается (это и есть M6-часть потребителя таблицы владения M5_AUDIT §6).
4. Мутации из таблицы §6: add / delete / enable / disable — **только через существующие db-функции** (CreateClient с транзакционным аллокатором адреса, SetClientEnabled, DeleteClient) + **перегенерация `awg0.conf` через `awgconf.Generate`** (та же атомарная цепочка tmp→fsync→rename→0600, что у CLI).
5. Download config: `awgconf.GenerateClient(handle, id)` — те же байты, что CLI печатает в stdout; **не пишется на диск**.
6. QR: та же клиентская конфигурация как изображение.
7. Статус интерфейса: `has_interface:false` → состояние «без интерфейса»; файл отсутствует → «na» (M5 §6-E).
8. Секреты: private/preshared ключи **никогда** не попадают в HTML, ответы, логи (инвариант M4/M5; шаблонная модель без key-полей).
9. `compose.yaml`: в M6 добавляется `ports:` для panel (DEPLOYMENT.md).
10. Exit/error семантика serve: 0 = чистое завершение по SIGTERM/SIGINT (прецедент: entrypoint awg, M1); 1 = фатальная ошибка; HTTP-ошибки — страница/редирект с generic-диагностикой, без эха секретов (конвенция CLI 0/1/2 + инвариант M4 «missing client id → явная ошибка, не silent no-op»).

### 2.2 Не специфицировано в источниках → PROPOSED (см. OPEN QUESTIONS Q3)

URL-таблица в ТЗ отсутствует (деталь форм server-side UI). Предложение минимального множества (формы + PRG, без JS):

```text
GET  /                     → dashboard (карточки + форма add)
POST /clients/new          → add (name, expiry опц.) → 303 /
POST /clients/{id}/enable  → 303 /
POST /clients/{id}/disable → 303 /
POST /clients/{id}/delete  → 303 /
GET  /clients/{id}/config  → скачивание (Content-Disposition: attachment)
GET  /clients/{id}/qr      → PNG
```

Bind/порт не определены в ТЗ. Предложение: флаг `serve [--addr host:port]`, дефолт `0.0.0.0:8787` внутри контейнера; хостовый mapping — Q2. Время: display в UTC (status.json уже UTC).

## 3. CURRENT STATE (что переиспользуется как есть)

- `internal/db`: `Open` (0600, `MaxOpenConns(1)`, `busy_timeout=5000` — сериализация записи решена), `Migrate`, `ServerRow`, `ClientsAll`, `ClientByID`, `CreateClient` (адрес+INSERT в одной транзакции), `SetClientEnabled`, `UpdateClientName`, `SetClientExpiry`, `DeleteClient`, `CreateServer`, `Get/SetSetting`; `ErrClientNotFound`/`ErrNoFreeAddress`/`ErrServerNotFound`; `ClientRecord.Expired()`.
- `internal/awgconf`: `Generate(handle, path)` (атомарно, 0600), `GenerateClient(handle, id)` (endpoint из `settings.endpoint`, зеркало J/S/H/I, DNS, полный IPv4-туннель), `WriteAtomic`, валидации.
- `internal/status`: `ReadStatus`, `ParseJSON` (строгий), модель без секретных полей, `Path()` (`AMNEZIA_STATUS_PATH`).
- `internal/keys`: stdlib X25519 (нужно для add).
- `internal/cli`: диспетчер уже содержит `case "serve"` → `cmdServe` (стаб, exit 1); `parseArgs`, `validateName`, `normalizeRFC3339`, `parseClientID` — валидаторы переиспользуются в HTTP-слое.
- `cli/status.go` (M5): consumer-команда есть; в M6 расширяется до HTML-отыгрыша.
- `main.go`: `os.Exit(cli.Run(...))` — менять не нужно.
- `compose.yaml`: у panel уже правильные mounts (data/config RW, status RO); нет `ports:`.
- Бонус: `github.com/dustin/go-humanize` уже в go.sum (indirect) — для RX/TX можно продвинуть в direct без новых загрузок.
- Пустые заготовки: `internal/web`, `internal/auth`, `internal/backup`, `internal/awg`, `internal/config`, `static/`, `templates/` (Dockerfile их НЕ копирует).

## 4. GAPS

1. `serve` — стаб (cli.go:196–198).
2. Нет web-пакета (заготовка пуста), нет шаблонов/ассетов; **panel Dockerfile копирует только бинарь** → шаблоны через `embed.FS` (Dockerfile не трогается) или COPY (трогается).
3. Нет QR-энкодера и **нет зависимости/пина под него** (Q1).
4. Нет `ports:` в compose; нет bind-адреса/порта (Q2).
5. Нет reconciliation-хелпера (join clients × peers, online ≤ 3 min).
6. Нет HTTP-контракта: URL-таблица, PRG, страницы ошибок (Q3).
7. Нет graceful-shutdown контракта serve (рекомендация: `http.Server.Shutdown`, SIGTERM → 0).
8. Нет лимитов/timeout (ReadHeaderTimeout, body limit) и security-заголовков — базовые, до M9.
9. Публикация порта в M6 открывает **неаутентифицированную** панель (auth = M7) — связка порт/bind (Q2).
10. Нет защиты от конкурентных мутаций в HTTP-слое (БД сериализуется сама, генерация конфига — вне транзакции; нужен мьютекс вокруг mutation+regenerate).

## 5. Конфликты с M3.2 / M5 / секретами (проверено)

- **M3.2**: перегенерация из M6 — тот же `awgconf.Generate`, что у CLI; mtime-поллинг awg увидит изменение штатно. `entrypoint.sh`/`syncconf.sh` M6 не трогает. Регресс: `test_m32.sh` 58/58.
- **M5 status.json**: panel читает RO (mount `:ro` + код-инвариант; писатель только `awgstatus`). Reconciliation — read-only. Нет конфликта. Регресс: `test_m5.sh` 57/57 ×3.
- **Секреты**: модель `status.Peer` без secret-полей; `ClientRecord` без private_key; в шаблон не передаётся секретный материал; error-страницы generic (конвенция `cli.fatal`).
- **Concurrent requests**: `MaxOpenConns(1)` + `busy_timeout` сериализуют SQLite; аллокация адреса транзакционна (add/add исключены на уровне БД). Остаётся мьютекс «мутация → Generate».
- **Атомарность DB → awg0.conf**: `WriteAtomic` (уникальный temp, fsync, rename, fsync dir, 0600); сбой Generate оставляет предыдущий конфиг.
- **CSRF/CORS/cookies**: в M6 нет сессий (M7) → CSRF-риска нет, CORS не нужен (non-JS, same-origin). Зафиксировать: в M7 с cookie-сессиями придёт CSRF-token.
- **Новые Go-зависимости**: только QR (Q1). Всё остальное — stdlib (`net/http`, `html/template`, `embed`, `net/url`).

## 6. OPEN QUESTIONS

**Q1. QR-энкодер.** В stdlib QR нет. (a) новая **запиненная** зависимость (например `github.com/skip2/go-qrcode`, MIT, pure Go) + запись в go.mod/go.sum (+ опционально в versions.lock для консистентности с политикой); (b) свой кодировщик (Byte mode, QR v1–10) на stdlib — ~400 строк + тест-векторы. ТЗ: "Go, mostly stdlib"; «no new modules» было ограничено критериями M2/M4. **Рекомендация: (a)**. Блокирует M6.5.

**Q2. Публикация порта и окно без аутентификации.** DEPLOYMENT.md требует `ports:` в M6, auth/сессий нет до M7 → публичная неаутентифицированная админ-панель. (a) mapping как есть; (b) `ports: ["127.0.0.1:8787:8787"]` — окно закрыто, в M7 открыть; (c) без mapping, доступ только в compose-сети. **Рекомендация: (b)**; влияет на SECURITY.md. Блокирует правку compose.

**Q3. URL-таблица и паритет CRUD.** ТЗ §6 перечисляет 5 операций (без rename/set-expiry, которые есть в CLI M4). Подтвердить таблицу §2.2 и включить ли в UI rename/set-expiry (**рекомендация: да** — паритет с CLI, иначе expiry нельзя выставить без ssh). Блокирует M6.3.

## 7. PROPOSED FILES

**NEW**

- `app/panel/internal/web/web.go` — маршрутизация, handler'ы, PRG, graceful shutdown, ошибки
- `app/panel/internal/web/reconcile.go` + `reconcile_test.go` — join clients × status, online ≤ 3 min, «na»
- `app/panel/internal/web/web_test.go` — httptest-матрица
- `app/panel/internal/web/templates/*.html` (`embed.FS`) и `static/style.css` (минимум)
- `app/panel/internal/web/security_test.go` — инварианты секретов/заголовков
- `app/panel/internal/qr/qr.go` + `qr_test.go` (по Q1)
- `app/panel/test_m6.sh` (fast: httptest без Docker), `app/panel/test_m6_live.sh` (compose E2E, шаблон test_m5_live.sh)
- `M6_AUDIT.md` (этот файл)

**MODIFIED**

- `app/panel/internal/cli/cli.go` — `cmdServe` (реальный; стаб удаляется)
- `compose.yaml` — `ports:` для panel (по Q2)
- `go.mod`/`go.sum` — QR-зависимость (по Q1)
- `docs/ARCHITECTURE.md` — §panel (HTTP-слой, reconciliation, контракт секретов)
- `docs/DEPLOYMENT.md` — §Ports (актуализация, по Q2)
- `docs/SECURITY.md` — окно аутентификации, рекомендованные header'ы
- `CHANGELOG.md` — только по отдельному указанию

**MUST NOT TOUCH**

- `app/awg/*` (entrypoint.sh, Dockerfile, syncconf.sh, test_m32*, test_m5*) — baseline M3.2/M5
- `app/panel/internal/status/*`, `app/panel/cmd/awgstatus/*`, `app/panel/internal/cli/status.go` — baseline M5
- `app/panel/internal/db/*`, `internal/awgconf/*`, `internal/keys/*` — baseline M2–M4, только вызов
- `internal/auth` — заготовка **строго M7**
- `versions.lock` — кроме фиксации QR-пина (Q1)
- `main.go` — не меняется

## 8. IMPLEMENTATION ORDER

```text
M6.1  Фундамент: internal/web (роутер, serve в cli, PRG, shutdown, ошибки),
      reconcile-хелпер; решение Q1–Q3
M6.2  Dashboard/карточки: шаблоны + reconciliation, online ≤ 3 min, RX/TX
M6.3  Мутации: add/delete/enable/disable (+rename/expiry по Q3) через db +
      regenerate; мьютекс мутация→конфиг
M6.4  compose ports (по Q2) + embed-контент; проверка Dockerfile
M6.5  Config download + QR (по Q1)
M6.6  Security-pass: лимиты, header'ы, инварианты секретов, SECURITY.md
M6.7  E2E (test_m6.sh + test_m6_live.sh) + полный регресс M3.2/M5 + docs
```

## 9. TEST MATRIX

- **unit**: reconcile (join, граница online ровно 3 min, отсутствие файла → «na», `has_interface:false`, peer без клиента, клиент без peer, expired-клиенты); шаблоны (инъекция `<script>` в name экранируется `html/template`); QR-декод (если Q1=a); config-download == байты `GenerateClient`.
- **API (httptest)**: GET / 200; несуществующий id → явная ошибка ≠ silent no-op (инвариант M4); валидация name/id (cli-валидаторы); 303-редиректы после POST; ошибки generic.
- **auth**: N/A в M6 (M7); проверить, что панель работает без cookie/сессий.
- **security**: ни в одном ответе нет private/preshared-ключей (скан HTML); tampered status.json → generic-ошибка; `X-Content-Type-Options` и пр., если введём; body-limit на POST.
- **concurrency**: N параллельных POST add → адреса уникальны, awg0.conf всегда валиден, GET во время мутаций — всегда 200.
- **integration SQLite**: мутация → `ClientsAll`/`ClientByID` согласованы; add+delete циклы.
- **integration awg0.conf**: после каждой мутации конфиг детерминированно перегенерирован (сравнение с `awgconf.Generate`).
- **M3.2 regression**: `test_m32.sh` 58/58; live: мутация → mtime → syncconf применил.
- **M5 regression**: `test_m5.sh` 57/57; panel не пишет в /status.
- **Docker E2E (live)**: panel-init → panel serve; curl: карточки; POST add → карточка + awg0.conf перегенерирован + `awg show` показывает peer; download == CLI-конфиг; QR = PNG; SIGTERM → exit 0.

## 10. SECURITY RISKS (M6-specific)

1. **Неаутентифицированный админ-UI** в окне M6→M7 (Q2 — главный риск).
2. Утечка секретов в шаблонах/логах/ошибках — инвариант + тест-скан.
3. Инъекции HTML — только `html/template` (autoscape), никакого raw-вывода ключей.
4. Логирование эндпоинтов с id — допустимо; конфигов/ключей — запрещено.
5. Slowloris/body-limit — базовые настройки `http.Server`, полный hardening M9.
6. Path-traversal: контент только из embed; скачивание по id из БД (не по пути); id — строгий int64 (`parseClientID`).
7. CSRF станет релевантен в M7 (cookie-сессии) — зафиксировать связку PRG+token в архитектуре.
8. DoS через QR-полезную нагрузку — конфиг фиксированного малого размера, риск ~0.

## 11. VERDICT

**READY** — с обязательным решением Q1–Q3 до соответствующих под-милестоунов (Q2 — до правки compose, Q1 — до M6.5, Q3 — до M6.3). Контракт возможностей полностью выводится из ТЗ §6+§10 и DEPLOYMENT.md; неспецифицированы только URL-детали и bind/порт (OPEN QUESTIONS). Реализация не начата; коммитов не было.

## 12. РЕАЛИЗАЦИЯ (M6.1–M6.7)

Коммиты: `a9ac8be` (M6.1–M6.3), плюс незакоммиченная работа M6.4–M6.6 (+`test_m6_live.sh`, M6.7).

**Решения по Q1–Q3**

- **Q1 = (a)**: `github.com/skip2/go-qrcode@v0.0.0-20200617195104-da1b6568686e` (CVSS ~0, pure Go, MIT, архивный) — пины в `go.mod`/`go.sum` + `versions.lock` (`QRCODE_GO`, `QRDECODE_GO`). Декодер (`makiuchi-d/gozxing`) — только тестовая зависимость. 256×256 px, конфиг как payload, генерация в памяти (без записи на диск).
- **Q2 = (b)**: `ports: ["127.0.0.1:8787:8787"]` (loopback-only; M7 после auth расширит mapping). Dockerfile не тронут — шаблоны в `embed.FS`.
- **Q3 = да**: rename/set-expiry в UI; полная URL-таблица §2.2 реализована.

**Состав (§2.1 против факта)**

1. `panel serve` — реальный HTTP-сервер (M6.1); graceful shutdown (SIGTERM → exit 0), recover-panic → generic 500.
2–4. Dashboard + reconciliation: join clients × peers по `public_key`, online = handshake ≤ 3 min (граница включена), peer без клиента скрыт, клиент без peer — offline, RX/TX человекочитаемы; состояние интерфейса (`Up/NA/Down/Err`) из статуса.
5. Мутации add/delete/enable/disable/rename/expiry — только через db-слой + `awgconf.Generate` (atomic 0600) под мьютексом; PRG (303), flash-сообщения; 404 для несуществующего id (инвариант M4 «явная ошибка»).
6. Config download `GET /clients/{id}/config` = байты `GenerateClient`, `Content-Disposition: attachment` с санитизированным именем, 404/413-семантика; рабочий (даже disabled/expired) — по ТЗ «generate on demand» (E2E сверяет байты download == CLI config).
7. QR `GET /clients/{id}/qr` → PNG (decode-тест; payload == конфигу).
8. M6.6: таймауты `http.Server` (5/10/15/60 s, MaxHeaderBytes 1 MiB), лимит тела 64 KiB → 413, security-заголовки (nosniff/DENY/no-referrer/CSP/no-store), инварианты секретов (unit-сканы + E2E шаг [7]); `docs/SECURITY.md` заполнен.
9. E2E `app/panel/test_m6_live.sh` (M6.7): panel-init → serve → add → download == CLI bytes → QR PNG → awg подхватил peer из awg0.conf → disable/enable → syncconf-цикл → сканы секретов. Регресс M3.2 (58/58) и M5 (57/57) — зелёные.

**Отклонения от аудита**

- `internal/awgconf/awgconf.go` (M4 baseline, «MUST NOT TOUCH»): добавлен заголовок `[Interface]` в рендер (E2E выявил: amneziawg-tools отклоняет конфиг без него) — минимальная правка рендера + 3 теста на точные байты; `app/awg/*` и M5-код не тронуты.
- Reconcile-хелпер собран в `internal/web/reconcile.go` (было задумано в `internal/status` — не тронут).

**Чего нет (корректно отсутствует)**: auth (M7), межконтейнерный HTTP (запрет ТЗ), изменения `internal/db`, `internal/keys`, `cli/status.go`, `cmd/awgstatus`, `app/awg/*`, `internal/auth`.

## 13. ИТОГОВЫЙ АУДИТ (независимый прогон по §12)

**VERDICT: PASS.** Соответствие контракту §2.1 (1–8) подтверждено: реализовано и покрыто тестами; единственная девиация — `[Interface]`-заголовок (§12) — обязательна для корректной работы панели и зафиксирована.

**Верификация (свежие прогоны):**

- `go test -race -count=1 ./...` — все 6 пакетов ok, гонок нет; web: 56 тестов (reconcile 10, mutations 11, ondemand 11, слой-хелперы 24).
- `go vet`, `gofmt -l`, `CGO_ENABLED=0 go build`, `git diff --check`, `docker compose --env-file versions.lock config --quiet` — чисто.
- Live E2E `test_m6_live.sh` — PASS (включая цикл disable/enable → syncconf и сканы секретов); стек и порт 8787 убраны после теста.
- Регресс M3.2 58/58, M5 57/57 (awg-код не менялся с момента последнего зелёного прогона).
- Границы §7 «MUST NOT TOUCH»: дифы `app/awg/*`, `cmd/awgstatus`, `internal/status`, `internal/db`, `internal/keys`, `internal/auth`, `cli/status.go`, `main.go` — пустые.

**Замечания по ходу аудита:**

- *Исправлено*: устаревший текст «Mutation handlers are wired in M6.3» в шаблоне (`index.html`) — удалён.
- *Исправлено*: в fail-сообщения E2E [6b] добавлена диагностика (число peers в status.json / секций в awg0.conf).
- *Принятые риски*: (1) `clientNew` — `CreateClient`+`SetClientExpiry` не атомарная пара (паритет CLI); (2) `mutate` — DB-запись и `Generate` не транзакционны, сбой регенерации → 500 при закоммиченной БД (семантика CLI, §5); (3) handshake в будущем (clock-skew) → «online» — безвредно; (4) косметика преаллокации карты в reconcile.
- *Наблюдение*: 1/4 транзиентный флейк [6b] (peer не убран за 40 s), не воспроизведён в 3 следующих прогонах; после диагностики harness'а причина фиксируется на месте при рецидиве.
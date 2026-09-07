#!/usr/bin/env bash
#
# update-agent.sh — привести развёртывание к выпуску (amnezia-vpn-server-nukf).
#
# ПОЧЕМУ ЮНИТ НА ХОСТЕ, А НЕ КОНТЕЙНЕР. Выпуск меняет правила nftables,
# параметры ядра, юниты systemd, значения в .env и строку сервера в базе.
# Контейнеру для этого нужен был бы постоянный root на хосте — а юниту он
# нужен только на время работы. И обновление обязано уметь поднять стек,
# который лежит, то есть не может быть его частью.
#
# ПОЧЕМУ АГЕНТ НЕ ВЕРИТ ЗАПРОСУ. Панель открыта в интернет по паролю, и её
# слово не должно быть приказом. Адрес репозитория зашит здесь, а не приходит
# в запросе; версия проверяется на существование и на то, что она новее
# установленной. Только вперёд: захвативший панель не должен уметь вернуть
# сервер на выпуск с известной дырой.
#
# ЧТО ЗАЩИЩАЕТ КОНТРОЛЬНАЯ СУММА. HTTPS отвечает на вопрос «с тем ли сервером
# я говорю», но не на вопрос «то ли это, что мы выпустили». Сумма берётся из
# тела выпуска, а архив — из вложений: подменивший файл должен ещё и
# отредактировать текст, который люди читают на странице выпуска и в панели.
#
# ПОРЯДОК. Снимок → скачать и сверить → установить → при неудаче вернуть
# прежний выпуск. База при откате НЕ откатывается: между началом обновления и
# отказом владелец мог добавить клиента, и потерять его хуже, чем остаться на
# новой схеме.

set -u

ROOT_DIR="${AMNEZIA_UPDATE_ROOT:-/opt/amnezia-vpn}"
REQUEST_FILE="${AMNEZIA_UPDATE_REQUEST:-${ROOT_DIR}/data/update-request.json}"
STATE_FILE="${AMNEZIA_UPDATE_STATE:-${ROOT_DIR}/status/update-state.json}"
LOG_FILE="${AMNEZIA_UPDATE_LOG:-${ROOT_DIR}/status/update-log.txt}"
LOCK_FILE="${AMNEZIA_UPDATE_LOCK:-${ROOT_DIR}/update-agent.lock}"
ROLLBACK_DIR="${AMNEZIA_UPDATE_ROLLBACK:-${ROOT_DIR}/.rollback}"
REPO="${AMNEZIA_UPDATE_REPO:-AndreyLear/amnezia-vpn-server}"
API_BASE="${AMNEZIA_UPDATE_API:-https://api.github.com}"
DOWNLOAD_BASE="${AMNEZIA_UPDATE_DOWNLOADS:-https://github.com}"
CURL_BIN="${AMNEZIA_UPDATE_CURL:-curl}"
WORK_DIR=""

now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

# Журнал переживает перезапуск, потому что обновление его и вызывает: без
# файла на диске владелец, чей стек только что поднялся заново, не узнал бы,
# на каком шаге всё пошло не так.
log() {
    printf 'update-agent: %s\n' "$*"
    [ -d "$(dirname "${LOG_FILE}")" ] \
        && printf '%s %s\n' "$(now)" "$*" >> "${LOG_FILE}" 2>/dev/null
    return 0
}

# Состояние — единственное, что читает панель. Пишется атомарно: панель может
# заглянуть в файл ровно в тот момент, когда мы его меняем.
write_state() { # write_state STATE STEP MESSAGE
    local dir tmp
    dir="$(dirname "${STATE_FILE}")"
    [ -d "${dir}" ] || return 0
    tmp="${STATE_FILE}.tmp"
    printf '{"schema":"v1","state":"%s","from":"%s","to":"%s","step":"%s","message":"%s","at_utc":"%s"}\n' \
        "$1" "${INSTALLED_VERSION:-}" "${WANTED_VERSION:-}" "$2" "$3" "$(now)" > "${tmp}" \
        && chmod 0644 "${tmp}" \
        && mv -f "${tmp}" "${STATE_FILE}"
    return 0
}

cleanup() {
    [ -n "${WORK_DIR}" ] && [ -d "${WORK_DIR}" ] && rm -rf "${WORK_DIR}"
    return 0
}
trap cleanup EXIT

# Отказ до того, как что-либо тронуто, и отказ после — разные вещи, но
# рассказываются одинаково: состояние, причина, и запрос убран, чтобы юнит по
# файлу не запустил нас снова с тем же результатом.
refuse() { # refuse STEP MESSAGE
    log "отказ на шаге $1: $2"
    write_state refused "$1" "$2"
    exit 1
}

installed_version() {
    sed -n 's/^IMAGE_VERSION=//p' "${ROOT_DIR}/versions.lock" 2>/dev/null | tail -1
}

# Версия — это три числа и ничего больше. Проверяется до всякого её
# употребления: дальше она попадёт в URL и в имя файла.
valid_version() { # valid_version STRING
    printf '%s' "$1" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'
}

# Строго новее. Равные версии — не ошибка, но и не работа.
is_newer() { # is_newer CANDIDATE CURRENT
    [ "$1" != "$2" ] || return 1
    [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -1)" = "$1" ]
}

# --- 0. один за раз ----------------------------------------------------
# Два install.sh в одном каталоге подерутся, и разбирать это придётся руками
# на живом сервере.
if : > "${LOCK_FILE}" 2>/dev/null && exec 9>>"${LOCK_FILE}"; then
    if command -v flock >/dev/null 2>&1 && ! flock -n 9; then
        # Запрос НЕ убирается: его хозяин — идущее обновление, и состояние,
        # которое сейчас видит панель, уже говорит человеку правду.
        log "обновление уже идёт; запрос отклонён"
        exit 1
    fi
else
    # Не притворяться, что обновление идёт: замок не взялся по другой
    # причине, и отказ по ней увёл бы разбирательство не туда.
    log "предупреждение: не удалось взять замок ${LOCK_FILE}; продолжаю без него"
fi

INSTALLED_VERSION="$(installed_version)"
WANTED_VERSION=""

[ -n "${INSTALLED_VERSION}" ] \
    || refuse "чтение развёртывания" "не удалось прочитать установленную версию"

# --- 1. запрос ---------------------------------------------------------
[ -f "${REQUEST_FILE}" ] || {
    log "запроса нет: ${REQUEST_FILE}"
    exit 0
}
WANTED_VERSION="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${REQUEST_FILE}" | head -1)"
# Запрос убирается сразу: юнит следит за появлением файла, и оставленный
# запрос запустил бы нас снова с тем же исходом.
rm -f "${REQUEST_FILE}"

valid_version "${WANTED_VERSION}" \
    || refuse "запрос" "версия в запросе не похожа на версию"

is_newer "${WANTED_VERSION}" "${INSTALLED_VERSION}" \
    || refuse "запрос" "версия ${WANTED_VERSION} не новее установленной ${INSTALLED_VERSION}"

log "запрошено обновление ${INSTALLED_VERSION} -> ${WANTED_VERSION}"
write_state running "запрос" "проверяю выпуск"

# --- 2. выпуск и контрольная сумма -------------------------------------
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/amnezia-update.XXXXXX")" \
    || refuse "подготовка" "не удалось создать рабочий каталог"

release_json="${WORK_DIR}/release.json"
code="$("${CURL_BIN}" -sSL --max-time 30 -H 'Accept: application/vnd.github+json' \
        -o "${release_json}" -w '%{http_code}' \
        "${API_BASE}/repos/${REPO}/releases/tags/v${WANTED_VERSION}" 2>/dev/null)" || code=""
case "${code}" in
    2??) ;;
    "" | 000) refuse "выпуск" "до GitHub не достучались" ;;
    *) refuse "выпуск" "выпуска v${WANTED_VERSION} нет (GitHub ответил ${code})" ;;
esac

# Строка машинная и с постоянным началом, поэтому её можно взять из ответа
# как есть — в отличие от прозы описания, которую разбирает панель.
WANTED_SHA="$(grep -Eo 'amnezia-sha256: [0-9a-f]{64}' "${release_json}" | head -1 | awk '{print $2}')"
[ -n "${WANTED_SHA}" ] \
    || refuse "выпуск" "в описании выпуска v${WANTED_VERSION} нет контрольной суммы"

# --- 3. исходники ------------------------------------------------------
write_state running "загрузка" "скачиваю исходники выпуска"
tarball="${WORK_DIR}/sources.tar.gz"
asset="amnezia-vpn-server-${WANTED_VERSION}.tar.gz"
code="$("${CURL_BIN}" -sSL --max-time 300 -o "${tarball}" -w '%{http_code}' \
        "${DOWNLOAD_BASE}/${REPO}/releases/download/v${WANTED_VERSION}/${asset}" 2>/dev/null)" || code=""
case "${code}" in
    2??) ;;
    "" | 000) refuse "загрузка" "до GitHub не достучались" ;;
    *) refuse "загрузка" "исходники выпуска не отдались (GitHub ответил ${code})" ;;
esac

GOT_SHA="$(sha256sum "${tarball}" 2>/dev/null | cut -d' ' -f1)"
[ "${GOT_SHA}" = "${WANTED_SHA}" ] \
    || refuse "проверка" "контрольная сумма исходников не совпала с объявленной в выпуске"
log "контрольная сумма совпала: ${WANTED_SHA}"

srcdir="${WORK_DIR}/src"
mkdir -p "${srcdir}"
tar -xzf "${tarball}" -C "${srcdir}" 2>/dev/null \
    || refuse "распаковка" "архив выпуска не распаковался"
installer="$(find "${srcdir}" -maxdepth 2 -name install.sh -type f | head -1)"
[ -n "${installer}" ] \
    || refuse "распаковка" "в архиве выпуска нет install.sh"
chmod 0755 "${installer}" 2>/dev/null || true

# --- 4. снимок ---------------------------------------------------------
# Снимается всё, из чего установщик собирает развёртывание, и сам установщик:
# без него откат пришлось бы качать по сети, которая на откате может быть
# ровно тем, что сломалось.
write_state running "снимок" "снимаю копию перед обновлением"
rm -rf "${ROLLBACK_DIR}"
mkdir -p "${ROLLBACK_DIR}" || refuse "снимок" "не удалось создать ${ROLLBACK_DIR}"
# Список — это ровно то, что установщик раскладывает по развёртыванию: он
# копирует их к себе из своего каталога и падает, не найдя любой. Снимок,
# в котором чего-то нет, — это откат, который не запустится.
SNAPSHOT_ITEMS="compose.yaml versions.lock docker-prune.sh watchdog.sh update-check.sh update-agent.sh install.sh app"
for item in .env ${SNAPSHOT_ITEMS}; do
    [ -e "${ROOT_DIR}/${item}" ] || continue
    cp -a "${ROOT_DIR}/${item}" "${ROLLBACK_DIR}/" \
        || refuse "снимок" "не удалось скопировать ${item}"
done
[ -f "${ROLLBACK_DIR}/install.sh" ] \
    || log "предупреждение: в развёртывании нет install.sh; откат будет невозможен"

# Копия базы — отдельно и средствами самой панели: она знает, как снять
# согласованный снимок работающего SQLite.
if (cd "${ROOT_DIR}" && docker compose --env-file versions.lock run --rm -T \
        panel-init /app/panel backup create >/dev/null 2>&1); then
    log "копия данных снята"
else
    log "предупреждение: копию данных снять не удалось; обновление продолжается"
fi

# --- 5. установка ------------------------------------------------------
# Без флагов: значения развёртывания установщик помнит в .env, и передавать
# их заново значило бы дать запросу из панели право их менять.
write_state running "установка" "ставлю выпуск ${WANTED_VERSION}"
log "запускаю ${installer} --root ${ROOT_DIR}"
if "${installer}" --root "${ROOT_DIR}" >> "${LOG_FILE}" 2>&1; then
    write_state ok "готово" "обновление до ${WANTED_VERSION} завершено"
    log "обновление до ${WANTED_VERSION} завершено"
    rm -rf "${ROLLBACK_DIR}"
    exit 0
fi

# --- 6. откат ----------------------------------------------------------
log "установка выпуска ${WANTED_VERSION} не удалась; возвращаю ${INSTALLED_VERSION}"
write_state running "откат" "возвращаю выпуск ${INSTALLED_VERSION}"

if [ ! -f "${ROLLBACK_DIR}/install.sh" ]; then
    write_state failed "откат" "обновление не удалось, и откатить нечем: сервер остался в промежуточном состоянии"
    log "ОТКАТ НЕВОЗМОЖЕН: в снимке нет install.sh"
    exit 1
fi

# Удалить и положить заново, а не переписать поверх: среди возвращаемого есть
# и этот самый скрипт. Удалённый файл живёт, пока его читает запущенная
# оболочка, а переписанный поверх — меняется у неё под ногами.
for item in .env ${SNAPSHOT_ITEMS}; do
    [ -e "${ROLLBACK_DIR}/${item}" ] || continue
    rm -rf "${ROOT_DIR}/${item}"
    cp -a "${ROLLBACK_DIR}/${item}" "${ROOT_DIR}/" \
        || log "предупреждение: при откате не восстановлен ${item}"
done

if "${ROLLBACK_DIR}/install.sh" --root "${ROOT_DIR}" >> "${LOG_FILE}" 2>&1; then
    write_state rolled-back "откат" "обновление до ${WANTED_VERSION} не удалось; сервер работает на ${INSTALLED_VERSION}"
    log "откат на ${INSTALLED_VERSION} прошёл; сервер работает"
    exit 1
fi

# Громко и без утешений: дальше нужен человек.
write_state failed "откат" "обновление не удалось, и откат тоже: сервер требует вмешательства"
log "ОТКАТ НЕ УДАЛСЯ: сервер требует вмешательства. Журнал: ${LOG_FILE}"
exit 1

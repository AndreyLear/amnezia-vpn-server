/**
 * Подписи серверов (amnezia-vpn-server-q1do.4).
 *
 * Имя основному серверу и каскадам задаёт владелец; пустое — подпись по
 * умолчанию, какой она была до этого: основной сервер без подписи, каскад
 * «Каскад» при одном узле и «Каскад <узел>» при нескольких. Имя — чужой
 * текст: его не склоняют и не согласуют, поэтому внутри фраз оно стоит
 * после слова «Каскад»/«Сервер» в кавычках (тексты — с редактором).
 */

type Named = { name: string; title?: string };

/** Подпись каскада сама по себе: строка шапки, кнопка маршрута. */
export function entryLabel(entry: Named, count: number): string {
  const title = entry.title?.trim();
  if (title) return title;
  return count > 1 ? `Каскад ${entry.name}` : "Каскад";
}

/** Каскад внутри фразы после слова «Каскад»: «Москва» в кавычках или узел. */
export function entryRef(entry: Named): string {
  const title = entry.title?.trim();
  return title ? `«${title}»` : entry.name;
}

/** Имя кнопки строки основного сервера в шапке. */
export function serverButtonLabel(name: string | undefined): string {
  const trimmed = name?.trim();
  return trimmed ? `Сервер «${trimmed}»` : "Сервер";
}

/** Предел длины имени — тот же, что у сервера (db.DisplayNameMaxRunes). */
export const displayNameMax = 32;

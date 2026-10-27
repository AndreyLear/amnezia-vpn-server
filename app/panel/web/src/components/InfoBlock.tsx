import type { ComponentProps } from "react";

import { cn } from "@/lib/utils";

/**
 * Блок окна клиента (amnezia-vpn-server-14y0.9): владелец попросил делить
 * содержимое на карточки вместо одной «простыни» строк — внутренний отступ
 * 16px, скругление 16px, рамка того же токена, что у полей и карточек
 * панели. Скругление задано явно: --radius у панели 10px, и ближайшие
 * ступени шкалы (xl — 14px, 2xl — 18px) мимо 16px.
 *
 * Строки внутри — со своими py-2 и разделителями; у первой и последней
 * отступ снят (first:pt-0 / last:pb-0 у строк), иначе к 16px блока
 * добавлялись бы ещё 8px.
 */
export function InfoBlock({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      data-slot="info-block"
      className={cn("grid gap-0 rounded-[1rem] border border-border p-4 text-sm", className)}
      {...props}
    />
  );
}

/** Строки блока: разделитель между ними, без лишнего отступа по краям. */
export const infoRowsClass = "grid divide-y divide-border gap-0";
export const infoRowClass = "py-2 first:pt-0 last:pb-0";

/**
 * Раздел внутри карточки — не своя карточка (amnezia-vpn-server-14y0.9).
 * Владелец: «Табы и контент табов внутри блока. Это всё один блок», поэтому
 * вкладки со всем содержимым — одна карточка, а её разделы (график, адрес,
 * настройки…) отделяются разделителем, как строки «общего». Родитель
 * раздела — infoRowsClass.
 */
export function InfoSection({ className, ...props }: ComponentProps<"div">) {
  return <div data-slot="info-section" className={cn(infoRowClass, className)} {...props} />;
}

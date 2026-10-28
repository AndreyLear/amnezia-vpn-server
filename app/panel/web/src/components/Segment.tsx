import type { ReactNode } from "react";
import { Tabs as TabsPrimitive } from "radix-ui";

import { buttonVariants } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/**
 * Сегмент — выбор одного из нескольких рядом стоящих вариантов
 * (amnezia-vpn-server-14y0.13). Один на всю панель, двух уровней:
 *
 * - md — первый уровень: протокол в окне клиента и в окне QR;
 * - sm — второй, вложенный в первый: период графика, маршрут VLESS.
 *
 * Образец владельца — переключатель графика «10 минут | Сутки»: по
 * содержимому и слева, а не полосой на всю ширину, как была вкладка
 * AWG|VLESS. Первый уровень крупнее второго, чтобы вложенность читалась.
 *
 * Выбранный вариант — фон и полный цвет текста, остальные приглушены: он
 * обязан читаться с первого взгляда (amnezia-vpn-server-14y0.8).
 *
 * tabs — сегмент как переключатель вкладок Radix (role tab, у вкладок есть
 * панели); значение тогда держит корень Tabs. Без tabs — кнопки с
 * aria-pressed: там переключается вид, а не панель.
 */
export type SegmentSize = "md" | "sm";

export type SegmentOption<T extends string> = { value: T; label: ReactNode };

type SegmentProps<T extends string> = {
  label: string;
  options: SegmentOption<T>[];
  value: T;
  size?: SegmentSize;
  className?: string;
} & ({ tabs: true; onValueChange?: never } | { tabs?: false; onValueChange: (value: T) => void });

const listClass = "inline-flex w-fit max-w-full flex-wrap items-center gap-1";

function itemClass(size: SegmentSize, active: boolean) {
  return cn(
    buttonVariants({ variant: active ? "secondary" : "ghost", size: size === "md" ? "default" : "sm" }),
    size === "md" && "px-3",
    // Под курсором ghost давал фон и полный цвет текста — невыбранный
    // вариант становился похож на выбранный, и в окне QR было не понять,
    // какой маршрут выбран (amnezia-vpn-server-14y0.17). Фон — признак
    // выбора и только его; наведение лишь проявляет текст.
    !active && "text-muted-foreground hover:bg-transparent hover:text-foreground dark:hover:bg-transparent",
  );
}

export function Segment<T extends string>(props: SegmentProps<T>) {
  const { label, options, value, size = "md", className } = props;
  const common = {
    "data-slot": "segment",
    "data-size": size,
    "aria-label": label,
    className: cn(listClass, className),
  };
  if (props.tabs) {
    return (
      <TabsPrimitive.List {...common}>
        {options.map((o) => (
          <TabsPrimitive.Trigger
            key={o.value}
            value={o.value}
            className={itemClass(size, o.value === value)}
          >
            {o.label}
          </TabsPrimitive.Trigger>
        ))}
      </TabsPrimitive.List>
    );
  }
  const { onValueChange } = props;
  return (
    <div role="group" {...common}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          aria-pressed={o.value === value}
          className={itemClass(size, o.value === value)}
          // Мышью фокус на кнопку не переносим (amnezia-vpn-server-14y0.17):
          // кнопка, оставшаяся в фокусе после клика, по первому же нажатию
          // клавиши — хоть Cmd+Shift+4 для скриншота — начинает подходить
          // под :focus-visible, и вокруг AWG/VLESS вставал толстый контур.
          // С клавиатуры фокус и контур — как раньше. Вкладки (tabs) так не
          // сделать: Radix переключает их как раз по mousedown.
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => onValueChange(o.value)}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

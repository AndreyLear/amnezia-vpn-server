"use client"

import { type CSSProperties } from "react"
import { Toaster as Sonner, type ToasterProps } from "sonner"
import { CircleCheckIcon, InfoIcon, TriangleAlertIcon, OctagonXIcon, Loader2Icon } from "lucide-react"

// Все тосты — сверху справа, на любой ширине (решение владельца,
// amnezia-vpn-server-1f31). Раньше на телефоне они стояли сверху по центру,
// а на компьютере — снизу справа, и одно и то же сообщение появлялось в
// разных местах. На узком экране Sonner сам растягивает тост во всю ширину.
// position в пропсах нет: место тостов решено один раз для всей панели, и
// переданное значение молча перебивалось бы (amnezia-vpn-server-o79j).
function Toaster({ ...props }: Omit<ToasterProps, "position">) {
  return (
    <Sonner
      theme="system"
      className="toaster group"
      icons={{
        success: <CircleCheckIcon className="size-4" />,
        info: <InfoIcon className="size-4" />,
        warning: <TriangleAlertIcon className="size-4" />,
        error: <OctagonXIcon className="size-4" />,
        loading: <Loader2Icon className="size-4 animate-spin" />,
      }}
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
          "--border-radius": "var(--radius)",
        } as CSSProperties
      }
      toastOptions={{
        classNames: {
          toast: "cn-toast",
        },
      }}
      {...props}
      position="top-right"
    />
  )
}

export { Toaster }

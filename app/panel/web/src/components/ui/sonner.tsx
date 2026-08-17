"use client"

import { useEffect, useState, type CSSProperties } from "react"
import { Toaster as Sonner, type ToasterProps } from "sonner"
import { CircleCheckIcon, InfoIcon, TriangleAlertIcon, OctagonXIcon, Loader2Icon } from "lucide-react"

const MAX_SM_QUERY = "(max-width: 639px)"

function useMaxSm() {
  const [maxSm, setMaxSm] = useState(
    () => typeof window !== "undefined" && window.matchMedia(MAX_SM_QUERY).matches,
  )

  useEffect(() => {
    const media = window.matchMedia(MAX_SM_QUERY)
    const sync = () => {
      setMaxSm(media.matches)
    }
    sync()
    media.addEventListener("change", sync)
    return () => media.removeEventListener("change", sync)
  }, [])

  return maxSm
}

function Toaster({ ...props }: ToasterProps) {
  const maxSm = useMaxSm()

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
      position={maxSm ? "top-center" : props.position}
    />
  )
}

export { Toaster }

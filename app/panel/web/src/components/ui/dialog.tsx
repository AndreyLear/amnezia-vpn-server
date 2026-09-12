import * as React from "react"
import { Dialog as DialogPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { XIcon } from "lucide-react"

function Dialog({
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Root>) {
  return <DialogPrimitive.Root data-slot="dialog" {...props} />
}

function DialogTrigger({
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Trigger>) {
  return <DialogPrimitive.Trigger data-slot="dialog-trigger" {...props} />
}

function DialogPortal({
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Portal>) {
  return <DialogPrimitive.Portal data-slot="dialog-portal" {...props} />
}

function DialogClose({
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Close>) {
  return <DialogPrimitive.Close data-slot="dialog-close" {...props} />
}

function DialogOverlay({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Overlay>) {
  return (
    <DialogPrimitive.Overlay
      data-slot="dialog-overlay"
      className={cn(
        "fixed inset-0 isolate z-50 bg-black/10 duration-100 supports-backdrop-filter:backdrop-blur-xs data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0",
        className
      )}
      {...props}
    />
  )
}

// A tabbable descendant other than the close button itself. Mirrors the
// element types Radix's own focus scope treats as tabbable closely enough
// for our purposes (amnezia-vpn-server-suni): we only need to know whether
// autofocusing the close button is about to happen, not reproduce Radix's
// full algorithm.
const FOCUSABLE_SELECTOR =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'

function DialogContent({
  className,
  children,
  showCloseButton = true,
  bodyClassName,
  closeButtonDisabled = false,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Content> & {
  showCloseButton?: boolean
  /**
   * Классы для прокручиваемого тела окна: отступы и сетка живут ЗДЕСЬ, а
   * className — на внешнем элементе, где живут ширина и положение
   * (amnezia-vpn-server-kq1m).
   *
   * Разделено, потому что короткое время className уходил в оба места
   * разом, и любая утилита отступа применялась дважды: pb-6 карточки
   * клиента давал 24px на внешнем элементе плюс 24px на теле, и под
   * последней кнопкой зияла пустая полоса.
   */
  bodyClassName?: string
  // amnezia-vpn-server-yjh2: a caller whose mutation is already in flight
  // (request sent, cannot be cancelled) passes this so the X visibly can't
  // be clicked instead of silently doing nothing — a disabled native button
  // never fires the click Radix's Close listens for, so this also blocks
  // the close itself, not just its look.
  closeButtonDisabled?: boolean
}) {
  return (
    <DialogPortal>
      <DialogOverlay />
      <DialogPrimitive.Content
        data-slot="dialog-content"
        className={cn(
          "fixed z-50 flex h-auto max-h-[calc(100dvh-2rem)] w-full max-w-[calc(100%-2rem)] flex-col overflow-hidden rounded-xl bg-popover text-sm text-popover-foreground ring-1 ring-foreground/10 duration-100 outline-none sm:top-1/2 sm:left-1/2 sm:max-w-sm sm:-translate-x-1/2 sm:-translate-y-1/2 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
          className,
          "max-sm:top-auto max-sm:right-0 max-sm:bottom-0 max-sm:left-0 max-sm:w-full max-sm:max-w-none max-sm:translate-x-0 max-sm:translate-y-0 max-sm:rounded-b-none max-sm:[&_[data-size=icon-sm]]:size-12"
        )}
        onOpenAutoFocus={(event) => {
          // Radix moves keyboard focus to the first tabbable descendant as
          // soon as the dialog mounts. In windows with nothing else to
          // focus (Журнал, Состояние служб, …) that descendant is the close
          // button, so a focus ring appeared around the X on every open —
          // Chromium matches :focus-visible for a script-driven focus()
          // call, not just real Tab presses. Dialogs that DO have a real
          // field to autofocus (an input, for the mobile keyboard) already
          // set their own onOpenAutoFocus and take precedence over this one
          // (amnezia-vpn-server-suni). When nothing but the close button
          // qualifies, send focus to the content panel instead: it carries
          // outline-none, so nothing is drawn, and a real Tab press
          // afterwards still lands on the close button with a normal,
          // visible ring.
          const container = event.currentTarget as HTMLElement
          const hasOwnFocusTarget = Array.from(
            container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)
          ).some((el) => !el.closest('[data-slot="dialog-close"]'))
          if (!hasOwnFocusTarget) {
            event.preventDefault()
            container.focus({ preventScroll: true })
          }
        }}
        {...props}
      >
        {/* Only this wrapper scrolls; the header (sticky, see DialogHeader)
            and the close button below stay put while it does. Tall content
            used to live directly in this element, which meant the header
            and the close button scrolled away with everything else — the
            title, the "10 минут / сутки" toggle and the X all drifted
            upward together on the client card (amnezia-vpn-server-5oj5).
            The close button sits outside this div entirely (a sibling, not
            a child) so it is never part of what scrolls.

            className is forwarded here too, not just to the outer element:
            the body owns the row gap now (gap-4 by default, same as the
            old single-element default), and a dialog that wants a
            different one (most pass gap-6) still gets to say so from the
            call site — it must not become the primitive's job to guess.
            Forwarding it only to the outer element silently stopped every
            such override from doing anything (the outer box has a single
            flow child, so its own gap has no effect), which is exactly
            what widened QrDialog and ClientInfoDialog's spacing by
            accident. tailwind-merge resolves any conflicting utility (say,
            BackupUploadDialog's overflow-hidden) in favour of whichever
            copy — outer's or this one's — the dialog actually meant;
            width utilities landing on both are simply redundant, since
            this wrapper already stretches to the outer element's width. */}
        <div
          data-slot="dialog-body"
          className={cn("grid gap-4 overflow-y-auto p-4", bodyClassName)}
        >
          {children}
        </div>
        {showCloseButton && (
          <DialogPrimitive.Close data-slot="dialog-close" asChild>
            <Button
              variant="ghost"
              // z-20, а не просто absolute: закреплённая шапка несёт z-10
              // и без этого рисуется ПОВЕРХ крестика — он оставался в
              // разметке, но исчезал с экрана во всех окнах сразу
              // (amnezia-vpn-server-jy87).
              className="absolute top-2 right-2 z-20"
              size="icon-sm"
              disabled={closeButtonDisabled}
            >
              <XIcon
              />
              <span className="sr-only">Close</span>
            </Button>
          </DialogPrimitive.Close>
        )}
      </DialogPrimitive.Content>
    </DialogPortal>
  )
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-header"
      className={cn(
        // Sticky, not the default static flow: DialogContent's body wrapper
        // is what scrolls now, and without this the header scrolled away
        // with it (amnezia-vpn-server-5oj5). top-0 pins it to that
        // wrapper's own scrollport; bg-popover keeps scrolled-under content
        // from showing through, and z-10 keeps it painted above that
        // content (both default to nothing special otherwise, since a
        // sticky item with z-index:auto paints in DOM order rather than
        // above later siblings). Harmless when the dialog is short enough
        // that nothing scrolls — sticky then behaves exactly like static.
        "sticky top-0 z-10 flex flex-col gap-2 bg-popover max-sm:pr-12",
        className
      )}
      {...props}
    />
  )
}

function DialogFooter({
  className,
  showCloseButton = false,
  children,
  ...props
}: React.ComponentProps<"div"> & {
  showCloseButton?: boolean
}) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn(
        "-mx-4 -mb-4 flex flex-col-reverse gap-2 rounded-b-xl border-t bg-muted/50 p-4 sm:flex-row sm:justify-end",
        className
      )}
      {...props}
    >
      {children}
      {showCloseButton && (
        <DialogPrimitive.Close asChild>
          <Button variant="outline">Close</Button>
        </DialogPrimitive.Close>
      )}
    </div>
  )
}

function DialogTitle({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Title>) {
  return (
    <DialogPrimitive.Title
      data-slot="dialog-title"
      className={cn(
        "font-heading text-base leading-none font-medium",
        className
      )}
      {...props}
    />
  )
}

function DialogDescription({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Description>) {
  return (
    <DialogPrimitive.Description
      data-slot="dialog-description"
      className={cn(
        "text-sm text-muted-foreground *:[a]:underline *:[a]:underline-offset-3 *:[a]:hover:text-foreground",
        className
      )}
      {...props}
    />
  )
}

export {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogOverlay,
  DialogPortal,
  DialogTitle,
  DialogTrigger,
}

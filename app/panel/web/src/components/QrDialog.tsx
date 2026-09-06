import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

type QrDialogProps = {
  clientId: number | null;
  clientName: string;
  onOpenChange: (open: boolean) => void;
};

export function QrDialog({ clientId, clientName, onOpenChange }: QrDialogProps) {
  return (
    <Dialog open={clientId !== null} onOpenChange={onOpenChange}>
      <DialogContent onOpenAutoFocus={(e) => e.preventDefault()}>
        <DialogHeader>
          <DialogTitle>QR-код: {clientName}</DialogTitle>
          {/* Названия приложений здесь — примеры, а не список: конфигурацию
              читает любой клиент, понимающий протокол. Ссылок нет намеренно —
              кому нужно, тот найдёт приложение по названию, а три адреса на
              две строки в маленьком окне мешают больше, чем помогают
              (amnezia-vpn-server-d86w).

              Уровень 2.0 назван не наугад: набор параметров, который мы
              выдаём (Jc/Jmin/Jmax, S1-S4, H1-H4, I1-I5), — это именно он
              (internal/awgconf/generator.go). Клиент постарше конфиг не
              прочтёт, поэтому цифра в подписи важнее вежливости. */}
          <p className="text-sm text-muted-foreground">
            Отсканируйте QR в AmneziaWG, WG Tunnel или другом приложении с
            поддержкой AmneziaWG 2.0 и выше
          </p>
        </DialogHeader>
        {clientId !== null ? (
          // The symbol fills the dialog instead of sitting in a fixed 256 px
          // box: a client config needs 89 modules, and a camera reading them
          // off a screen needs every pixel of pitch it can get (T-ky6l). The
          // PNG is rendered far larger than shown, so the browser only ever
          // scales it down.
          <img
            className="mx-auto aspect-square w-full"
            alt={`QR-код клиента ${clientName}`}
            src={`/clients/${clientId}/qr`}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

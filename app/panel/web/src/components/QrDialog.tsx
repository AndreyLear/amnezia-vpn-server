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
          <p className="text-sm text-muted-foreground">
            Отсканируйте код в приложении AmneziaVPN
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

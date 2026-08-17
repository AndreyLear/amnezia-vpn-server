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
          <img
            className="mx-auto size-64"
            width={256}
            height={256}
            alt={`QR-код клиента ${clientName}`}
            src={`/clients/${clientId}/qr`}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

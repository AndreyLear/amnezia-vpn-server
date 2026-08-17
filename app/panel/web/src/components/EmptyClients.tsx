import { PlusIcon } from "lucide-react";

import { BackupMenu } from "@/components/BackupMenu";
import { Button } from "@/components/ui/button";

type EmptyClientsProps = {
  onAdd: () => void;
  restorePending?: boolean;
};

export function EmptyClients({ onAdd, restorePending = false }: EmptyClientsProps) {
  return (
    <div className="flex flex-row items-center gap-2">
      <Button type="button" onClick={onAdd}>
        <PlusIcon />
        Добавить клиента
      </Button>
      <BackupMenu restorePending={restorePending} />
    </div>
  );
}

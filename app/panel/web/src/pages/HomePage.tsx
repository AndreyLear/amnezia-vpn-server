import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";

import { AddClientDialog } from "@/components/AddClientDialog";
import { AppShell } from "@/components/AppShell";
import { AddClientCard, ClientCard } from "@/components/ClientCard";
import { ClientInfoDialog } from "@/components/ClientInfoDialog";
import { QrDialog } from "@/components/QrDialog";
import {
  api,
  mutationSucceeded,
  setCsrf,
  type Client,
  type MeResponse,
  type MutationResponse,
} from "@/lib/api";

export default function HomePage() {
  const [clients, setClients] = useState<Client[]>([]);
  const [totpEnabled, setTotpEnabled] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [infoId, setInfoId] = useState<number | null>(null);
  const [qrId, setQrId] = useState<number | null>(null);
  const [pendingId, setPendingId] = useState<number | "new" | null>(null);

  const load = useCallback(async () => {
    const list = await api<unknown>("/api/clients");
    if (Array.isArray(list)) setClients(list);
  }, []);

  useEffect(() => {
    let stopped = false;

    async function boot() {
      const me = await api<MeResponse>("/api/me");
      setCsrf(me.csrf);
      if (!stopped) {
        setTotpEnabled(me.totp.enabled);
        await load();
      }
    }

    void boot();
    const timer = window.setInterval(() => {
      if (!stopped) void load();
    }, 5000);

    return () => {
      stopped = true;
      window.clearInterval(timer);
    };
  }, [load]);

  const infoClient = clients.find((c) => c.id === infoId) ?? null;
  const qrClient = clients.find((c) => c.id === qrId);

  function downloadConfig(id: number) {
    window.location.assign(`/clients/${id}/config`);
  }

  async function createClient(payload: { name: string; description: string }) {
    setPendingId("new");
    try {
      const data = await api<MutationResponse>("/api/clients", {
        method: "POST",
        body: JSON.stringify(payload),
      });
      if (!mutationSucceeded(data)) return;
      toast.success("Клиент добавлен");
      setAddOpen(false);
      await load();
    } finally {
      setPendingId(null);
    }
  }

  async function toggleClient(client: Client) {
    setPendingId(client.id);
    try {
      const data = await api<MutationResponse>(`/api/clients/${client.id}`, {
        method: "PATCH",
        body: JSON.stringify({ enabled: !client.enabled }),
      });
      if (!mutationSucceeded(data)) return;
      toast.success(client.enabled ? "Клиент отключён" : "Клиент включён");
      await load();
    } finally {
      setPendingId(null);
    }
  }

  async function deleteClient(client: Client) {
    setPendingId(client.id);
    try {
      const data = await api<MutationResponse>(`/api/clients/${client.id}`, {
        method: "DELETE",
      });
      if (!mutationSucceeded(data)) return;
      toast.success("Клиент удалён");
      setInfoId(null);
      await load();
    } finally {
      setPendingId(null);
    }
  }

  return (
    <AppShell totpEnabled={totpEnabled} onTotpChange={setTotpEnabled}>
      <div data-testid="client-grid" className="grid grid-cols-1 gap-4 pb-8 min-[752px]:grid-cols-2">
        <AddClientCard onClick={() => setAddOpen(true)} />
        {clients.map((client) => (
          <ClientCard
            key={client.id}
            client={client}
            pending={pendingId === client.id}
            onInfo={() => setInfoId(client.id)}
            onQr={() => setQrId(client.id)}
            onDownload={() => downloadConfig(client.id)}
            onToggle={() => void toggleClient(client)}
            onDelete={() => void deleteClient(client)}
          />
        ))}
      </div>
      <AddClientDialog
        open={addOpen}
        pending={pendingId === "new"}
        onOpenChange={setAddOpen}
        onSubmit={createClient}
      />
      <ClientInfoDialog
        client={infoClient}
        pending={infoClient ? pendingId === infoClient.id : false}
        onOpenChange={(open) => {
          if (!open) setInfoId(null);
        }}
        onQr={() => infoClient && setQrId(infoClient.id)}
        onDownload={() => infoClient && downloadConfig(infoClient.id)}
        onToggle={() => infoClient && void toggleClient(infoClient)}
        onDelete={() => infoClient && void deleteClient(infoClient)}
      />
      <QrDialog
        clientId={qrId}
        clientName={qrClient?.name ?? ""}
        onOpenChange={(open) => {
          if (!open) setQrId(null);
        }}
      />
    </AppShell>
  );
}

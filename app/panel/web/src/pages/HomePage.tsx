import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";

import { AddClientDialog } from "@/components/AddClientDialog";
import { AppShell } from "@/components/AppShell";
import { ClientCard } from "@/components/ClientCard";
import { ClientInfoDialog } from "@/components/ClientInfoDialog";
import { QrDialog } from "@/components/QrDialog";
import {
  api,
  mutationSucceeded,
  setCsrf,
  setLastUsername,
  type Client,
  type HostSnapshot,
  type MeResponse,
  type MutationResponse,
} from "@/lib/api";

export default function HomePage() {
  const [clients, setClients] = useState<Client[]>([]);
  const [restorePending, setRestorePending] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [infoId, setInfoId] = useState<number | null>(null);
  const [qrId, setQrId] = useState<number | null>(null);
  const [pendingId, setPendingId] = useState<number | "new" | null>(null);
  const [host, setHost] = useState<HostSnapshot | null>(null);

  const load = useCallback(async () => {
    const list = await api<unknown>("/api/clients");
    if (Array.isArray(list)) setClients(list);
    try {
      const snap = await api<HostSnapshot>("/api/stats/host");
      if (snap && typeof snap === "object") {
        setHost({
          cpu_percent: snap.cpu_percent ?? null,
          ram_percent: snap.ram_percent ?? null,
          disk_percent: snap.disk_percent ?? null,
        });
      }
    } catch {
      // keep dashes; do not toast
    }
  }, []);

  useEffect(() => {
    let stopped = false;

    async function boot() {
      const me = await api<MeResponse>("/api/me");
      setCsrf(me.csrf);
      if (me.username) setLastUsername(me.username);
      if (!stopped) {
        setRestorePending(Boolean(me.restore_pending));
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
      const enabled = !client.enabled;
      setClients((list) =>
        list.map((c) => (c.id === client.id ? { ...c, enabled } : c)),
      );
      toast.success(client.enabled ? "Клиент отключён" : "Клиент включён");
      await load();
      setClients((list) =>
        list.map((c) => (c.id === client.id ? { ...c, enabled } : c)),
      );
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

  async function saveClientInfo(payload: { name: string; description: string }) {
    if (!infoClient) return false;
    setPendingId(infoClient.id);
    try {
      const data = await api<MutationResponse>(`/api/clients/${infoClient.id}`, {
        method: "PATCH",
        body: JSON.stringify(payload),
      });
      if (!mutationSucceeded(data)) return false;
      toast.success("Информация о пользователе была изменена.");
      await load();
      return true;
    } finally {
      setPendingId(null);
    }
  }

  return (
    <AppShell
      restorePending={restorePending}
      onAddClient={() => setAddOpen(true)}
      host={host}
    >
      <div
        data-testid="client-grid"
        className="mt-6 gap-2 sm:gap-x-3 pb-8 max-sm:flex max-sm:flex-col max-sm:pb-28 sm:grid sm:grid-cols-[minmax(0,1fr)_auto_auto_auto_auto]"
      >
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
        onSave={saveClientInfo}
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

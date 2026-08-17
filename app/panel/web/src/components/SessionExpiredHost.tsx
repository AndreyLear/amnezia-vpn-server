import { useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  api,
  completeSessionRelogin,
  getLastUsername,
  setCsrf,
  setLastUsername,
  subscribeSessionExpired,
  type LoginResponse,
  type MeResponse,
  type SessionLossReason,
} from "@/lib/api";

const copy: Record<SessionLossReason, { title: string; description: string }> = {
  idle: {
    title: "Сессия истекла",
    description: "Введите пароль, чтобы продолжить",
  },
  replaced: {
    title: "Вход с другого устройства",
    description: "Эта сессия закрыта. Если это были не вы — смените пароль через CLI",
  },
  gone: {
    title: "Сессия сброшена",
    description:
      "Панель перезапустили или пароль сменили на сервере. Введите пароль, чтобы продолжить",
  },
};

function reloginError(message?: string): string {
  if (!message) return "";
  if (/имя пользователя|логин/i.test(message)) return "Неверный пароль";
  return message;
}

export function SessionExpiredHost() {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState<SessionLossReason>("idle");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const submitting = useRef(false);
  const openRef = useRef(false);

  useEffect(() => {
    return subscribeSessionExpired((next, nextReason) => {
      if (next) {
        if (nextReason) setReason(nextReason);
        if (!openRef.current) {
          setPassword("");
          setError("");
        }
        openRef.current = true;
        setOpen(true);
        return;
      }
      openRef.current = false;
      setOpen(false);
    });
  }, []);

  async function submit() {
    if (submitting.current) return;
    if (!password.trim()) {
      setError("");
      return;
    }
    submitting.current = true;
    setPending(true);
    setError("");
    try {
      const res = await api<LoginResponse>("/api/login", {
        method: "POST",
        body: JSON.stringify({
          username: getLastUsername(),
          password,
        }),
      });
      if (!res.ok) {
        setError(reloginError(res.message));
        return;
      }
      const meRes = await fetch("/api/me", { credentials: "same-origin" });
      if (meRes.ok) {
        const me = (await meRes.json()) as MeResponse;
        if (me.csrf) setCsrf(me.csrf);
        if (me.username) setLastUsername(me.username);
      }
      completeSessionRelogin();
      setOpen(false);
      openRef.current = false;
    } finally {
      submitting.current = false;
      setPending(false);
    }
  }

  const text = copy[reason];

  return (
    <Dialog open={open} onOpenChange={() => {}}>
      <DialogContent
        showCloseButton={false}
        onPointerDownOutside={(e) => e.preventDefault()}
        onInteractOutside={(e) => e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>{text.title}</DialogTitle>
          <DialogDescription>{text.description}</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <div className="grid gap-2">
            <Label htmlFor="session-expired-password">Пароль</Label>
            <Input
              id="session-expired-password"
              name="password"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              disabled={pending}
            />
          </div>
          {error ? <p className="text-sm text-destructive">{error}</p> : null}
          <DialogFooter>
            <Button type="submit" disabled={pending}>
              Повторить вход
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

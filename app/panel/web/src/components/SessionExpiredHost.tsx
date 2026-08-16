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
} from "@/lib/api";

export function SessionExpiredHost() {
  const [open, setOpen] = useState(false);
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [needCode, setNeedCode] = useState(false);
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const submitting = useRef(false);

  useEffect(() => {
    return subscribeSessionExpired((next) => {
      setOpen(next);
      if (next) {
        setPassword("");
        setCode("");
        setNeedCode(false);
        setError("");
      }
    });
  }, []);

  async function submit() {
    if (submitting.current) return;
    submitting.current = true;
    setPending(true);
    setError("");
    try {
      const res = await api<LoginResponse>("/api/login", {
        method: "POST",
        body: JSON.stringify({
          username: getLastUsername(),
          password,
          code,
        }),
      });
      if (res.need_code) {
        setNeedCode(true);
        return;
      }
      if (!res.ok) {
        setError(res.message ?? "");
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
    } finally {
      submitting.current = false;
      setPending(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={() => {}}>
      <DialogContent
        showCloseButton={false}
        onPointerDownOutside={(e) => e.preventDefault()}
        onInteractOutside={(e) => e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>Нужно войти заново</DialogTitle>
          <DialogDescription>Сессия истекла. Введите пароль, чтобы продолжить.</DialogDescription>
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
          {needCode ? (
            <div className="grid gap-2">
              <Label htmlFor="session-expired-totp">Код 2FA</Label>
              <Input
                id="session-expired-totp"
                name="code"
                inputMode="numeric"
                autoComplete="one-time-code"
                value={code}
                onChange={(e) => setCode(e.target.value)}
                disabled={pending}
              />
            </div>
          ) : null}
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

import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { api, mutationSucceeded, setCsrf, type MeResponse, type MutationResponse } from "@/lib/api";

type PasswordDialogProps = {
  open: boolean;
  totpEnabled: boolean;
  onOpenChange: (open: boolean) => void;
  onTotpChange: (enabled: boolean) => void;
};

type EnrollResponse = MutationResponse & {
  qr?: string;
  otpauth?: string;
  secret?: string;
};

export function PasswordDialog({
  open,
  totpEnabled,
  onOpenChange,
  onTotpChange,
}: PasswordDialogProps) {
  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [code, setCode] = useState("");
  const [enrollPassword, setEnrollPassword] = useState("");
  const [qr, setQr] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);

  useEffect(() => {
    if (!open) {
      setOldPassword("");
      setNewPassword("");
      setConfirmPassword("");
      setCode("");
      setEnrollPassword("");
      setQr("");
      setError("");
    }
  }, [open]);

  async function refreshMe() {
    const me = await api<MeResponse>("/api/me");
    setCsrf(me.csrf);
    onTotpChange(me.totp.enabled);
  }

  async function changePassword() {
    setPending(true);
    setError("");
    try {
      const data = await api<MutationResponse>("/api/account/password", {
        method: "POST",
        body: JSON.stringify({
          old_password: oldPassword,
          new_password: newPassword,
          confirm_password: confirmPassword,
          code,
        }),
      });
      if (!mutationSucceeded(data)) {
        setError(data?.message ?? "");
        return;
      }
      toast.success(data.message ?? "Пароль изменён.");
      await refreshMe();
      onOpenChange(false);
    } finally {
      setPending(false);
    }
  }

  async function enroll() {
    setPending(true);
    setError("");
    try {
      const data = await api<EnrollResponse>("/api/account/totp/enroll", {
        method: "POST",
        body: JSON.stringify({ password: enrollPassword || oldPassword }),
      });
      if (!mutationSucceeded(data) || !data.qr) {
        setError(data?.message ?? "");
        return;
      }
      setQr(data.qr);
    } finally {
      setPending(false);
    }
  }

  async function confirmTotp() {
    setPending(true);
    setError("");
    try {
      const data = await api<MutationResponse>("/api/account/totp/confirm", {
        method: "POST",
        body: JSON.stringify({
          password: enrollPassword || oldPassword,
          code,
        }),
      });
      if (!mutationSucceeded(data)) {
        setError(data?.message ?? "");
        return;
      }
      toast.success(data.message ?? "2FA включена.");
      setQr("");
      setCode("");
      await refreshMe();
    } finally {
      setPending(false);
    }
  }

  async function disableTotp() {
    setPending(true);
    setError("");
    try {
      const data = await api<MutationResponse>("/api/account/totp/disable", {
        method: "POST",
        body: JSON.stringify({
          password: enrollPassword || oldPassword,
          code,
        }),
      });
      if (!mutationSucceeded(data)) {
        setError(data?.message ?? "");
        return;
      }
      toast.success(data.message ?? "2FA отключена.");
      setCode("");
      await refreshMe();
    } finally {
      setPending(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Изменить пароль</DialogTitle>
        </DialogHeader>
        <form
          className="grid gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            void changePassword();
          }}
        >
          <div className="grid gap-2">
            <Label htmlFor="old-password">Текущий пароль</Label>
            <Input
              id="old-password"
              type="password"
              autoComplete="current-password"
              value={oldPassword}
              onChange={(e) => setOldPassword(e.target.value)}
              disabled={pending}
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="new-password">Новый пароль</Label>
            <Input
              id="new-password"
              type="password"
              autoComplete="new-password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              disabled={pending}
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="confirm-password">Повтор нового пароля</Label>
            <Input
              id="confirm-password"
              type="password"
              autoComplete="new-password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              disabled={pending}
            />
          </div>
          {totpEnabled ? (
            <div className="grid gap-2">
              <Label htmlFor="password-totp">Код 2FA</Label>
              <Input
                id="password-totp"
                inputMode="numeric"
                autoComplete="one-time-code"
                value={code}
                onChange={(e) => setCode(e.target.value)}
                disabled={pending}
              />
            </div>
          ) : null}
          <div className="flex items-center justify-between gap-3">
            <Label htmlFor="totp-enabled">Двухфакторная аутентификация</Label>
            <Switch
              id="totp-enabled"
              checked={totpEnabled || Boolean(qr)}
              disabled={pending}
              onCheckedChange={(next) => {
                if (next) void enroll();
                else void disableTotp();
              }}
            />
          </div>
          {!totpEnabled ? (
            <div className="grid gap-2">
              <Label htmlFor="enroll-password">Пароль для 2FA</Label>
              <Input
                id="enroll-password"
                type="password"
                value={enrollPassword}
                onChange={(e) => setEnrollPassword(e.target.value)}
                disabled={pending}
              />
            </div>
          ) : null}
          {qr ? (
            <div className="grid justify-items-center gap-2">
              <img src={qr} alt="QR-код 2FA" className="size-40" />
              <Label htmlFor="confirm-totp">Код подтверждения</Label>
              <Input
                id="confirm-totp"
                inputMode="numeric"
                value={code}
                onChange={(e) => setCode(e.target.value)}
                disabled={pending}
              />
              <Button type="button" disabled={pending} onClick={() => void confirmTotp()}>
                Подтвердить 2FA
              </Button>
            </div>
          ) : null}
          {error ? <p className="text-sm text-destructive">{error}</p> : null}
          <DialogFooter>
            <Button type="submit" disabled={pending}>
              Сохранить пароль
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

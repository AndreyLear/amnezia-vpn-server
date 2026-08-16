import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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
  const [passwordTotpCode, setPasswordTotpCode] = useState("");
  const [enrollPassword, setEnrollPassword] = useState("");
  const [disablePassword, setDisablePassword] = useState("");
  const [disableCode, setDisableCode] = useState("");
  const [confirmCode, setConfirmCode] = useState("");
  const [qr, setQr] = useState("");
  const [secret, setSecret] = useState("");
  const [passwordError, setPasswordError] = useState("");
  const [totpError, setTotpError] = useState("");
  const [pending, setPending] = useState(false);

  useEffect(() => {
    if (!open) {
      setOldPassword("");
      setNewPassword("");
      setPasswordTotpCode("");
      setEnrollPassword("");
      setDisablePassword("");
      setDisableCode("");
      setConfirmCode("");
      setQr("");
      setSecret("");
      setPasswordError("");
      setTotpError("");
    }
  }, [open]);

  async function refreshMe() {
    const me = await api<MeResponse>("/api/me");
    setCsrf(me.csrf);
    onTotpChange(me.totp.enabled);
  }

  async function changePassword() {
    if (oldPassword.trim() === "" && newPassword.trim() === "") {
      onOpenChange(false);
      return;
    }

    setPending(true);
    setPasswordError("");
    try {
      const data = await api<MutationResponse>("/api/account/password", {
        method: "POST",
        body: JSON.stringify({
          old_password: oldPassword,
          new_password: newPassword,
          confirm_password: newPassword,
          code: passwordTotpCode,
        }),
      });
      if (!mutationSucceeded(data)) {
        setPasswordError(data?.message ?? "");
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
    if (enrollPassword.trim() === "") {
      setTotpError(
        "Чтобы включить 2FA, подтвердите текущим паролем. Это не смена пароля аккаунта.",
      );
      return;
    }

    setPending(true);
    setTotpError("");
    try {
      const data = await api<EnrollResponse>("/api/account/totp/enroll", {
        method: "POST",
        body: JSON.stringify({ password: enrollPassword }),
      });
      if (!mutationSucceeded(data) || !data.qr) {
        setTotpError(data?.message ?? "");
        return;
      }
      setQr(data.qr);
      setSecret(data.secret ?? "");
    } finally {
      setPending(false);
    }
  }

  async function confirmTotp() {
    setPending(true);
    setTotpError("");
    try {
      const data = await api<MutationResponse>("/api/account/totp/confirm", {
        method: "POST",
        body: JSON.stringify({
          password: enrollPassword,
          code: confirmCode,
        }),
      });
      if (!mutationSucceeded(data)) {
        setTotpError(data?.message ?? "");
        return;
      }
      toast.success(data.message ?? "2FA включена.");
      setQr("");
      setSecret("");
      setConfirmCode("");
      await refreshMe();
    } finally {
      setPending(false);
    }
  }

  async function disableTotp() {
    if (disablePassword.trim() === "" || disableCode.trim() === "") {
      setTotpError("Чтобы выключить 2FA, введите текущий пароль и код из приложения.");
      return;
    }

    setPending(true);
    setTotpError("");
    try {
      const data = await api<MutationResponse>("/api/account/totp/disable", {
        method: "POST",
        body: JSON.stringify({
          password: disablePassword,
          code: disableCode,
        }),
      });
      if (!mutationSucceeded(data)) {
        setTotpError("Неверный пароль или код.");
        return;
      }
      toast.success(data.message ?? "2FA отключена.");
      setDisableCode("");
      await refreshMe();
    } finally {
      setPending(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Аккаунт</DialogTitle>
        </DialogHeader>
        <form
          className="grid gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            void changePassword();
          }}
        >
          <h3 className="font-heading text-sm font-medium">Пароль</h3>
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
          {totpEnabled ? (
            <div className="grid gap-2">
              <Label htmlFor="password-totp">Код 2FA</Label>
              <Input
                id="password-totp"
                inputMode="numeric"
                autoComplete="one-time-code"
                value={passwordTotpCode}
                onChange={(e) => setPasswordTotpCode(e.target.value)}
                disabled={pending}
              />
            </div>
          ) : null}
          {passwordError ? <p className="text-sm text-destructive">{passwordError}</p> : null}
          <div>
            <Button type="submit" disabled={pending}>
              Сохранить пароль
            </Button>
          </div>
        </form>
        <div className="grid gap-3 border-t pt-4">
          <div className="flex items-baseline justify-between gap-3">
            <h3 className="font-heading text-sm font-medium">Двухфакторная аутентификация</h3>
            <p className="text-muted-foreground text-sm">{totpEnabled ? "вкл" : "выкл"}</p>
          </div>
          {!totpEnabled ? (
            <>
              {!qr ? (
                <p className="text-muted-foreground text-sm">
                  QR появится после подтверждения паролем.
                </p>
              ) : null}
              <div className="grid gap-2">
                <Label htmlFor="totp-enroll-password">
                  Текущий пароль{" "}
                  <span className="text-muted-foreground font-normal">для включения 2FA</span>
                </Label>
                <Input
                  id="totp-enroll-password"
                  type="password"
                  autoComplete="current-password"
                  value={enrollPassword}
                  onChange={(e) => setEnrollPassword(e.target.value)}
                  disabled={pending}
                />
              </div>
              {!qr ? (
                <div>
                  <Button type="button" disabled={pending} onClick={() => void enroll()}>
                    Включить
                  </Button>
                </div>
              ) : (
                <div className="grid justify-items-center gap-2">
                  <img src={qr} alt="QR-код 2FA" className="size-40" />
                  {secret ? (
                    <p className="font-mono text-sm break-all">
                      <span className="text-muted-foreground">Секрет: </span>
                      {secret}
                    </p>
                  ) : null}
                  <Label htmlFor="confirm-totp">Код подтверждения</Label>
                  <Input
                    id="confirm-totp"
                    inputMode="numeric"
                    value={confirmCode}
                    onChange={(e) => setConfirmCode(e.target.value)}
                    disabled={pending}
                  />
                  <Button type="button" disabled={pending} onClick={() => void confirmTotp()}>
                    Подтвердить 2FA
                  </Button>
                </div>
              )}
            </>
          ) : null}
          {totpEnabled ? (
            <>
              <div className="grid gap-2">
                <Label htmlFor="totp-disable-password">
                  Текущий пароль{" "}
                  <span className="text-muted-foreground font-normal">для отключения 2FA</span>
                </Label>
                <Input
                  id="totp-disable-password"
                  type="password"
                  autoComplete="current-password"
                  value={disablePassword}
                  onChange={(e) => setDisablePassword(e.target.value)}
                  disabled={pending}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="totp-disable-code">Код из приложения</Label>
                <Input
                  id="totp-disable-code"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  value={disableCode}
                  onChange={(e) => setDisableCode(e.target.value)}
                  disabled={pending}
                />
              </div>
              <div>
                <Button type="button" disabled={pending} onClick={() => void disableTotp()}>
                  Выключить
                </Button>
              </div>
            </>
          ) : null}
          {totpError ? <p className="text-sm text-destructive">{totpError}</p> : null}
        </div>
      </DialogContent>
    </Dialog>
  );
}

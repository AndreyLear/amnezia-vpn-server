import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";

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
import { Spinner } from "@/components/ui/spinner";
import { api, type MailInfo, type MailSaveError, type MailSavePayload } from "@/lib/api";
import { formatHandshake } from "@/lib/format";

/**
 * Окно «Уведомления» (amnezia-vpn-server-8fg2, dfs2).
 *
 * Пять полей почты и пробное письмо. Письма отправляет не панель, а служба на
 * сервере, поэтому пробное письмо — просьба: окно оставляет её и ждёт ответа,
 * пока смотрит человек.
 *
 * Не дошло — настройки всё равно сохранены, но окно говорит об отказе словами
 * и показывает ответ почтового сервера. Запрещать сохранение нельзя: сервер
 * бывает недоступен десять минут. Притворяться, что всё хорошо, тоже нельзя:
 * канал, который есть только на бумаге, хуже честного «не настроено».
 */

/** Как часто спрашиваем об ответе и сколько ждём. */
const POLL_MS = 2000;
const WAIT_MS = 90_000;

type Field = keyof MailSavePayload;

const EMPTY: MailSavePayload = { host: "", port: 587, username: "", password: "", recipient: "" };

/** Ответ, похожий на настройки почты; всё прочее окно не принимает. */
function isMailInfo(value: unknown): value is MailInfo {
  return Boolean(value && typeof value === "object" && "configured" in value && "test" in value);
}

function formFrom(info: MailInfo): MailSavePayload {
  return {
    host: info.host,
    port: info.port || 587,
    username: info.username,
    password: "",
    recipient: info.recipient,
  };
}

function Ellipsis() {
  return (
    <span className="animated-ellipsis" aria-hidden>
      <span>.</span>
      <span>.</span>
      <span>.</span>
    </span>
  );
}

/** Строка о пробном письме и о том, проверена ли настройка. */
function MailStatus({ info, now }: { info: MailInfo; now: number }) {
  if (!info.configured) return null;
  if (!info.password_set) {
    return (
      <p className="text-sm text-destructive">
        Пароль не сохранён: после восстановления из бэкапа его нужно ввести заново
      </p>
    );
  }
  const { test } = info;
  // Письмо правил, от которого служба отказалась после всех повторов, — позже
  // пробного: о нём и говорим (amnezia-vpn-server-pz2r).
  const failure = info.last_failure;
  if (
    info.channel === "failing" &&
    failure &&
    test.state !== "pending" &&
    (test.state !== "failed" || !test.at_utc || failure.at_utc >= test.at_utc)
  ) {
    return (
      <div className="grid gap-1 text-sm">
        <p className="text-destructive">
          «{failure.subject}» не отправлено {formatHandshake(failure.at_utc)}
        </p>
        <p className="break-words font-mono text-xs text-muted-foreground">{failure.error}</p>
      </div>
    );
  }
  if (test.state === "pending") {
    const requested = test.requested_at_utc ? new Date(test.requested_at_utc).getTime() : now;
    if (now - requested > WAIT_MS) {
      return (
        <p className="text-sm text-destructive">
          Сервер не ответил за {WAIT_MS / 1000} секунд, пробное письмо не отправлено. Проверьте
          состояние служб.
        </p>
      );
    }
    return (
      <p className="text-sm text-muted-foreground">
        Отправляем пробное письмо
        <Ellipsis />
      </p>
    );
  }
  if (test.state === "failed") {
    return (
      <div className="grid gap-1 text-sm">
        <p className="text-destructive">Пробное письмо не ушло. Настройки сохранены, но не проверены.</p>
        {test.error ? (
          <p className="break-words font-mono text-xs text-muted-foreground">{test.error}</p>
        ) : null}
      </div>
    );
  }
  if (test.state === "ok" && info.verified) {
    return (
      <p className="text-sm text-muted-foreground">
        Пробное письмо отправлено {formatHandshake(test.at_utc ?? null)}
      </p>
    );
  }
  if (!info.verified) {
    return <p className="text-sm text-muted-foreground">Настройки не проверены</p>;
  }
  return null;
}

function FieldError({ name, message }: { name: Field; message?: string }) {
  return message ? (
    <p id={`mail-${name}-error`} className="text-sm text-destructive">
      {message}
    </p>
  ) : null;
}

export function NotificationsDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [info, setInfo] = useState<MailInfo | null>(null);
  const [form, setForm] = useState<MailSavePayload>(EMPTY);
  const [saved, setSaved] = useState<MailSavePayload>(EMPTY);
  const [errors, setErrors] = useState<Partial<Record<Field, string>>>({});
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  // Настройки приходят с сервера не мгновенно. Пока их нет, поля закрыты:
  // иначе пришедший ответ затирал уже набранное, и сохранялась пустая форма
  // (amnezia-vpn-server-2pdq, поймано на тестовом сервере).
  const [loaded, setLoaded] = useState(false);
  const hostRef = useRef<HTMLInputElement>(null);

  function accept(next: MailInfo) {
    setInfo(next);
    const values = formFrom(next);
    setForm(values);
    setSaved(values);
  }

  useEffect(() => {
    if (!open) return;
    let alive = true;
    setErrors({});
    setLoaded(false);
    void api<MailInfo>("/api/mail").then((next) => {
      if (!alive) return;
      if (isMailInfo(next)) accept(next);
      setLoaded(true);
    });
    return () => {
      alive = false;
    };
  }, [open]);

  // Фокус — в первое поле, когда оно открылось для ввода.
  useEffect(() => {
    if (open && loaded) hostRef.current?.focus();
  }, [open, loaded]);

  // Пока пробное письмо в пути, спрашиваем об ответе. Форму при этом не
  // трогаем: человек мог начать править поле.
  const pending = info?.test.state === "pending";
  useEffect(() => {
    if (!open || !pending) return;
    let alive = true;
    const timer = window.setInterval(() => {
      setNow(Date.now());
      void api<MailInfo>("/api/mail").then((next) => {
        if (alive && isMailInfo(next)) setInfo(next);
      });
    }, POLL_MS);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, [open, pending]);

  const dirty =
    form.host !== saved.host ||
    form.port !== saved.port ||
    form.username !== saved.username ||
    form.password !== "" ||
    form.recipient !== saved.recipient;

  function set<K extends Field>(key: K, value: MailSavePayload[K]) {
    setForm((prev) => ({ ...prev, [key]: value }));
    setErrors((prev) => ({ ...prev, [key]: undefined }));
  }

  async function save() {
    setSaving(true);
    try {
      const res = await api<MailInfo | MailSaveError>("/api/mail", {
        method: "PUT",
        body: JSON.stringify(form),
      });
      if (isMailInfo(res)) {
        accept(res);
        setNow(Date.now());
        toast.success("Настройки сохранены");
        return;
      }
      const failure = res as MailSaveError | undefined;
      if (failure?.field) {
        setErrors({ [failure.field]: failure.message ?? "Проверьте поле" });
        return;
      }
      toast.error(failure?.message || "Не удалось сохранить настройки");
    } finally {
      setSaving(false);
    }
  }

  async function sendTest() {
    setTesting(true);
    try {
      const res = await api<MailInfo | MailSaveError>("/api/mail/test", { method: "POST" });
      if (isMailInfo(res)) {
        setInfo(res);
        setNow(Date.now());
        return;
      }
      toast.error((res as MailSaveError | undefined)?.message || "Не удалось отправить пробное письмо");
    } finally {
      setTesting(false);
    }
  }

  const busy = saving || testing || !loaded;
  const canTest = Boolean(info?.configured && info.password_set) && !dirty && !pending && !busy;

  function fieldProps(key: Field) {
    return {
      id: `mail-${key}`,
      name: key,
      disabled: busy,
      "aria-invalid": errors[key] ? true : undefined,
      "aria-describedby": errors[key] ? `mail-${key}-error` : undefined,
    };
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        bodyClassName="gap-6"
        onOpenAutoFocus={(e) => {
          e.preventDefault();
          hostRef.current?.focus();
        }}
      >
        <DialogHeader>
          <DialogTitle>Уведомления</DialogTitle>
          <DialogDescription>
            Письма о сбоях и обновлениях уходят через ваш почтовый ящик
          </DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-6"
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <div className="grid gap-4">
            {/* items-start: ошибка под сервером растягивала строку, и поле
                порта съезжало вниз вслед за ней (amnezia-vpn-server-2pdq). */}
            <div className="grid items-start gap-4 sm:grid-cols-[1fr_6rem]">
              <div className="grid gap-2">
                <Label htmlFor="mail-host">Сервер SMTP</Label>
                <Input
                  ref={hostRef}
                  {...fieldProps("host")}
                  placeholder="smtp.example.com"
                  autoComplete="off"
                  value={form.host}
                  onChange={(e) => set("host", e.target.value)}
                />
                <FieldError name="host" message={errors.host} />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="mail-port">Порт</Label>
                <Input
                  {...fieldProps("port")}
                  inputMode="numeric"
                  value={form.port ? String(form.port) : ""}
                  onChange={(e) => set("port", Number(e.target.value.replace(/\D/g, "")) || 0)}
                />
                <FieldError name="port" message={errors.port} />
              </div>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="mail-username">Логин</Label>
              <Input
                {...fieldProps("username")}
                autoComplete="off"
                placeholder="name@example.com"
                value={form.username}
                onChange={(e) => set("username", e.target.value)}
              />
              <FieldError name="username" message={errors.username} />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="mail-password">Пароль</Label>
              <Input
                {...fieldProps("password")}
                type="password"
                autoComplete="new-password"
                placeholder={info?.password_set ? "Сохранён" : ""}
                value={form.password}
                onChange={(e) => set("password", e.target.value)}
              />
              <FieldError name="password" message={errors.password} />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="mail-recipient">Куда присылать</Label>
              <Input
                {...fieldProps("recipient")}
                type="email"
                autoComplete="email"
                placeholder="name@example.com"
                value={form.recipient}
                onChange={(e) => set("recipient", e.target.value)}
              />
              <FieldError name="recipient" message={errors.recipient} />
            </div>
          </div>
          <div aria-live="polite">{info ? <MailStatus info={info} now={now} /> : null}</div>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={!canTest}
              onClick={() => void sendTest()}
              className="max-sm:h-12 max-sm:w-full"
            >
              {testing ? <Spinner /> : null}
              Отправить пробное письмо
            </Button>
            <Button type="submit" disabled={busy} className="max-sm:h-12 max-sm:w-full">
              {saving ? <Spinner /> : null}
              Сохранить
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

import { useEffect, useState } from "react";
import { Pencil } from "lucide-react";
import { toast } from "sonner";

import { ReadOnlyProperty } from "@/components/ClientInfoDialog";
import { InfoBlock, infoRowsClass } from "@/components/InfoBlock";
import { UserText, noTranslate } from "@/components/UserText";
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
import { api, mutationOk, type MutationResponse } from "@/lib/api";
import { displayNameMax, entryLabel } from "@/lib/serverNames";

/**
 * Окно сервера по клику на его строку в шапке (amnezia-vpn-server-q1do.4).
 *
 * Владелец просил, чтобы имена серверам давал он, а не панель. Окно — по
 * образцу окна клиента: строка «Имя» с кнопкой «Изменить» и то же окно
 * правки с одним полем. У каскада ещё строка «Узел» — техническое имя, по
 * которому к нему обращаются команды CLI: имя владельца его не меняет.
 *
 * Пустое имя — подпись по умолчанию, и подсказка под полем называет её
 * прямо: «без имени» у основного, «Каскад»/«Каскад entry» у каскада.
 * Обрезку пробелов, длину и уникальность проверяет сервер; окно ограничивает
 * только длину поля, чтобы не упираться в отказ после «Сохранить».
 */
export type ServerTarget =
  | { kind: "server"; name: string }
  | { kind: "entry"; entry: { name: string; title?: string }; count: number };

const saveButtonClass = "max-sm:h-12 max-sm:w-full";

function currentName(target: ServerTarget): string {
  return target.kind === "server" ? target.name.trim() : (target.entry.title?.trim() ?? "");
}

export function ServerDialog({
  target,
  onOpenChange,
  onSaved,
}: {
  target: ServerTarget | null;
  onOpenChange: (open: boolean) => void;
  /** Имя сохранено: шапке пора перечитать снимок. */
  onSaved?: () => void | Promise<void>;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [pending, setPending] = useState(false);
  // Пока шапка не перечитала снимок, окно показывает сохранённое, а не
  // прежнее имя.
  const [saved, setSaved] = useState<string | null>(null);

  const key = target ? (target.kind === "server" ? "server" : `entry:${target.entry.name}`) : "";
  useEffect(() => {
    setEditing(false);
    setSaved(null);
  }, [key]);

  const name = target ? (saved ?? currentName(target)) : "";
  useEffect(() => {
    if (target && saved !== null && currentName(target) === saved) setSaved(null);
  }, [target, saved]);

  function startEdit() {
    setDraft(name);
    setEditing(true);
  }

  async function save() {
    if (!target || pending) return;
    if (draft.trim() === name) {
      setEditing(false);
      return;
    }
    setPending(true);
    try {
      const [path, body] =
        target.kind === "server"
          ? ["/api/server/name", { name: draft }]
          : [`/api/entries/${encodeURIComponent(target.entry.name)}/title`, { title: draft }];
      const data = await api<MutationResponse>(path, { method: "PUT", body: JSON.stringify(body) });
      if (!mutationOk(data)) return;
      const next = draft.trim();
      setSaved(next);
      setEditing(false);
      toast.success(next ? "Имя сохранено" : "Имя сброшено");
      await onSaved?.();
    } catch {
      toast.error("Не удалось сохранить имя");
    } finally {
      setPending(false);
    }
  }

  const hint =
    target?.kind === "entry"
      ? `До ${displayNameMax} символов. Пустое — «${entryLabel({ name: target.entry.name }, target.count)}»`
      : `До ${displayNameMax} символов. Пустое — без имени`;

  return (
    <>
      <Dialog open={target !== null} onOpenChange={onOpenChange}>
        <DialogContent className="sm:max-w-md" bodyClassName="gap-6 pb-6">
          {target ? (
            <>
              <DialogHeader>
                <DialogTitle>{target.kind === "server" ? "Сервер" : "Каскад"}</DialogTitle>
              </DialogHeader>
              <InfoBlock>
                <dl className={infoRowsClass}>
                  <ReadOnlyProperty
                    label="Имя"
                    actions={
                      <Button type="button" variant="outline" aria-label="Изменить имя" onClick={startEdit}>
                        <Pencil data-icon="inline-start" aria-hidden />
                        Изменить
                      </Button>
                    }
                  >
                    <dd>
                      {name ? (
                        <UserText className="break-words">{name}</UserText>
                      ) : (
                        <span className="text-muted-foreground">Не задано</span>
                      )}
                    </dd>
                  </ReadOnlyProperty>
                  {target.kind === "entry" ? (
                    <div className="grid gap-0.5 py-2 first:pt-0 last:pb-0">
                      <dt className="text-muted-foreground">Узел</dt>
                      <dd {...noTranslate}>{target.entry.name}</dd>
                    </div>
                  ) : null}
                </dl>
              </InfoBlock>
            </>
          ) : null}
        </DialogContent>
      </Dialog>
      <Dialog
        open={editing}
        onOpenChange={(open) => {
          if (!open && !pending) setEditing(false);
        }}
      >
        <DialogContent className="sm:max-w-md" bodyClassName="gap-6">
          <DialogHeader>
            <DialogTitle>Имя</DialogTitle>
          </DialogHeader>
          <form
            className="grid gap-6"
            onSubmit={(e) => {
              e.preventDefault();
              void save();
            }}
          >
            <div className="grid gap-2">
              <Label htmlFor="server-name">Имя</Label>
              <Input
                id="server-name"
                maxLength={displayNameMax}
                autoComplete="off"
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                disabled={pending}
                aria-describedby="server-name-hint"
              />
              <p id="server-name-hint" className="text-sm text-muted-foreground">
                {hint}
              </p>
            </div>
            <DialogFooter>
              <Button type="submit" className={saveButtonClass} aria-label="Сохранить имя" disabled={pending}>
                Сохранить
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}

import { useState } from "react";

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
import { Textarea } from "@/components/ui/textarea";

export type AddClientPayload = {
  name: string;
  description: string;
};

type AddClientDialogProps = {
  open: boolean;
  pending?: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (payload: AddClientPayload) => void | Promise<void>;
};

export function AddClientDialog({
  open,
  pending = false,
  onOpenChange,
  onSubmit,
}: AddClientDialogProps) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          setName("");
          setDescription("");
        }
        onOpenChange(next);
      }}
    >
      <DialogContent className="gap-6">
        <DialogHeader>
          <DialogTitle>Добавить клиента</DialogTitle>
        </DialogHeader>
        <form
          className="grid gap-6"
          onSubmit={(e) => {
            e.preventDefault();
            void onSubmit({ name, description });
          }}
        >
          <div className="grid gap-4">
            <div className="grid gap-2">
              <Label htmlFor="client-name">Имя</Label>
              <Input
                id="client-name"
                name="name"
                required
                maxLength={64}
                value={name}
                onChange={(e) => setName(e.target.value)}
                disabled={pending}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="client-description">
                Описание{" "}
                <span className="font-normal text-muted-foreground">(опционально)</span>
              </Label>
              <Textarea
                id="client-description"
                name="description"
                rows={1}
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                disabled={pending}
              />
            </div>
          </div>
          <DialogFooter>
            <Button type="submit" disabled={pending}>
              Добавить
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

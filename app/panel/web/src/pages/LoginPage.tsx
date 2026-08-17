import { useEffect, useState } from "react";

import { AmbientBackground } from "@/components/AmbientBackground";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api, type LoginResponse } from "@/lib/api";

export default function LoginPage() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const res = await fetch("/api/me", { credentials: "same-origin" });
      if (!cancelled && res.ok) {
        window.location.assign("/");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  async function submit() {
    setPending(true);
    try {
      const res = await api<LoginResponse>("/api/login", {
        method: "POST",
        body: JSON.stringify({ username, password }),
      });
      if (!res.ok) {
        setError(res.message ?? "");
        return;
      }
      window.location.assign("/");
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="relative min-h-svh">
      <AmbientBackground />
      <main className="relative flex min-h-svh items-center justify-center p-6">
      <form
        className="grid w-full max-w-sm gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <div className="grid gap-2">
          <Label htmlFor="username">Имя пользователя</Label>
          <Input
            id="username"
            name="username"
            autoComplete="username"
            required
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            disabled={pending}
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="password">Пароль</Label>
          <Input
            id="password"
            name="password"
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={pending}
          />
        </div>
        {error ? <p className="text-sm text-destructive">{error}</p> : null}
        <Button type="submit" size="lg" className="mt-4" disabled={pending}>
          Войти
        </Button>
      </form>
      </main>
    </div>
  );
}

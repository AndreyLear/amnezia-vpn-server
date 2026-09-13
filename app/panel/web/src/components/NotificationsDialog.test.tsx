import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { NotificationsDialog } from "@/components/NotificationsDialog";
import { setCsrf, type MailInfo } from "@/lib/api";

// Окно «Уведомления» (amnezia-vpn-server-8fg2): настройки доезжают до
// сервера, пароль после сохранения не виден, неудача пробного письма видна
// словами, а не молчанием.

function mail(over: Partial<MailInfo> = {}): MailInfo {
  return {
    ok: true,
    configured: false,
    host: "",
    port: 587,
    username: "",
    recipient: "",
    password_set: false,
    verified: false,
    test: { state: "none" },
    channel: "off",
    ...over,
  };
}

const saved = mail({
  configured: true,
  host: "smtp.example.org",
  username: "vpn@example.org",
  recipient: "owner@example.org",
  password_set: true,
});

let current: MailInfo;
let putReply: unknown;
let requests: { method: string; url: string; body?: string }[];

beforeEach(() => {
  setCsrf("csrf");
  current = mail();
  putReply = undefined;
  requests = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const method = (init?.method ?? "GET").toUpperCase();
      requests.push({ method, url, body: init?.body as string | undefined });
      let body: unknown = current;
      let status = 200;
      if (method === "PUT") {
        body = putReply ?? current;
        status = (putReply as { ok?: boolean } | undefined)?.ok === false ? 400 : 200;
      }
      return new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("уведомления", () => {
  it("сохраняет поля и не показывает пароль после сохранения", async () => {
    const user = userEvent.setup();
    render(<NotificationsDialog open onOpenChange={() => {}} />);
    const host = await screen.findByLabelText("Сервер SMTP");
    await waitFor(() => expect(host).toBeEnabled());
    await user.type(host, "smtp.example.org");
    await user.type(screen.getByLabelText("Логин"), "vpn@example.org");
    await user.type(screen.getByLabelText("Пароль"), "secret-pass");
    await user.type(screen.getByLabelText("Куда присылать"), "owner@example.org");
    putReply = { ...saved, test: { state: "pending", requested_at_utc: new Date().toISOString() } };
    await user.click(screen.getByRole("button", { name: "Сохранить" }));

    await waitFor(() => expect(requests.some((r) => r.method === "PUT")).toBe(true));
    const put = requests.find((r) => r.method === "PUT")!;
    expect(put.url).toBe("/api/mail");
    expect(JSON.parse(put.body!)).toEqual({
      host: "smtp.example.org",
      port: 587,
      username: "vpn@example.org",
      password: "secret-pass",
      recipient: "owner@example.org",
    });
    const password = screen.getByLabelText("Пароль") as HTMLInputElement;
    await waitFor(() => expect(password.value).toBe(""));
    expect(password.placeholder).toBe("Сохранён");
    expect(screen.getByText("Оставьте пустым, чтобы не менять")).toBeInTheDocument();
    expect(screen.getByText(/Отправляем пробное письмо/)).toBeInTheDocument();
  });

  // Ответ с настройками пришёл позже, чем начали вводить, и затёр набранное —
  // сохранилась пустая форма (amnezia-vpn-server-2pdq, тестовый сервер).
  it("не даёт вводить, пока настройки не пришли, и не затирает ввод", async () => {
    const user = userEvent.setup();
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const plain = globalThis.fetch;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init?: RequestInit) => {
        await gate;
        return plain(url, init);
      }),
    );
    render(<NotificationsDialog open onOpenChange={() => {}} />);
    const host = screen.getByLabelText("Сервер SMTP");
    expect(host).toBeDisabled();
    expect(screen.getByRole("button", { name: "Сохранить" })).toBeDisabled();

    release();
    await waitFor(() => expect(host).toBeEnabled());
    await user.type(host, "smtp.example.org");
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(host).toHaveValue("smtp.example.org");
  });

  it("показывает отказ пробного письма и ответ почтового сервера", async () => {
    current = {
      ...saved,
      test: { state: "failed", error: "535 5.7.8 Authentication failed", at_utc: new Date().toISOString() },
    };
    render(<NotificationsDialog open onOpenChange={() => {}} />);
    expect(await screen.findByText(/Пробное письмо не ушло/)).toBeInTheDocument();
    expect(screen.getByText("535 5.7.8 Authentication failed")).toBeInTheDocument();
  });

  // Письмо правил не ушло после всех повторов — окно называет его
  // (amnezia-vpn-server-pz2r).
  it("называет письмо, от которого служба отказалась", async () => {
    current = {
      ...saved,
      verified: true,
      test: { state: "ok", at_utc: "2026-09-14T01:00:00Z" },
      channel: "failing",
      last_failure: { subject: "Туннель не работает 5 минут", error: "535 auth", at_utc: "2026-09-14T02:00:00Z" },
    };
    render(<NotificationsDialog open onOpenChange={() => {}} />);
    expect(await screen.findByText(/«Туннель не работает 5 минут» не отправлено/)).toBeInTheDocument();
    expect(screen.getByText("535 auth")).toBeInTheDocument();
  });

  it("подсвечивает поле, которое отверг сервер", async () => {
    const user = userEvent.setup();
    render(<NotificationsDialog open onOpenChange={() => {}} />);
    const host = await screen.findByLabelText("Сервер SMTP");
    await waitFor(() => expect(host).toBeEnabled());
    putReply = { ok: false, field: "recipient", message: "Укажите адрес почты" };
    await user.click(screen.getByRole("button", { name: "Сохранить" }));
    const recipient = screen.getByLabelText("Куда присылать");
    await waitFor(() => expect(recipient).toHaveAttribute("aria-invalid", "true"));
    expect(screen.getByText("Укажите адрес почты")).toBeInTheDocument();
  });

  it("говорит, что пароль нужно ввести заново после восстановления", async () => {
    current = { ...saved, password_set: false };
    render(<NotificationsDialog open onOpenChange={() => {}} />);
    expect(await screen.findByText(/нужно ввести заново/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Отправить пробное письмо" })).toBeDisabled();
  });

  it("пробное письмо — по кнопке, и окно дожидается ответа", async () => {
    const user = userEvent.setup();
    current = { ...saved, verified: true, test: { state: "ok", at_utc: new Date().toISOString() } };
    render(<NotificationsDialog open onOpenChange={() => {}} />);
    const button = await screen.findByRole("button", { name: "Отправить пробное письмо" });
    await waitFor(() => expect(button).toBeEnabled());

    current = { ...saved, test: { state: "pending", requested_at_utc: new Date().toISOString() } };
    await user.click(button);
    expect(requests.some((r) => r.method === "POST" && r.url === "/api/mail/test")).toBe(true);
    expect(await screen.findByText(/Отправляем пробное письмо/)).toBeInTheDocument();

    // Ответ сервера приходит при следующем опросе.
    current = { ...saved, verified: true, test: { state: "ok", at_utc: new Date().toISOString() } };
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 2100));
    });
    expect(await screen.findByText(/Пробное письмо отправлено/)).toBeInTheDocument();
  }, 10_000);

  it("не даёт отправить пробное письмо по несохранённым полям", async () => {
    const user = userEvent.setup();
    current = { ...saved, verified: true, test: { state: "ok", at_utc: new Date().toISOString() } };
    render(<NotificationsDialog open onOpenChange={() => {}} />);
    const button = await screen.findByRole("button", { name: "Отправить пробное письмо" });
    await waitFor(() => expect(button).toBeEnabled());
    await user.type(screen.getByLabelText("Куда присылать"), "x");
    expect(button).toBeDisabled();
  });
});

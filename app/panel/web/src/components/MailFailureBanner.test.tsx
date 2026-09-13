import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { MailFailureBanner } from "@/components/MailFailureBanner";
import { setCsrf, type MailInfo } from "@/lib/api";

// Полоса «письма не уходят» (amnezia-vpn-server-pz2r): не настроено и
// сломалось — разные вещи, и разница обязана читаться.

const base: MailInfo = {
  ok: true,
  configured: true,
  host: "smtp.example.org",
  port: 587,
  username: "vpn@example.org",
  recipient: "owner@example.org",
  password_set: true,
  verified: true,
  test: { state: "ok", at_utc: "2026-09-14T01:00:00Z" },
  channel: "ok",
};

let current: MailInfo;

beforeEach(() => {
  setCsrf("csrf");
  current = base;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(current), { headers: { "Content-Type": "application/json" } })),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

async function settled() {
  await waitFor(() => expect(vi.mocked(fetch)).toHaveBeenCalled());
  await new Promise((resolve) => setTimeout(resolve, 20));
}

describe("полоса «письма не уходят»", () => {
  it("молчит, когда почта не настроена: это не ошибка", async () => {
    current = { ...base, configured: false, password_set: false, verified: false, test: { state: "none" }, channel: "off" };
    render(<MailFailureBanner />);
    await settled();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each(["ok", "unverified"] as const)("молчит при канале %s", async (channel) => {
    current = { ...base, channel };
    render(<MailFailureBanner />);
    await settled();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("говорит, какое письмо не ушло и что ответил сервер", async () => {
    current = {
      ...base,
      channel: "failing",
      last_failure: { subject: "Туннель не работает 5 минут", error: "535 5.7.8 auth failed", at_utc: "2026-09-14T02:00:00Z" },
    };
    render(<MailFailureBanner />);
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Письма о сбоях не уходят");
    expect(alert).toHaveTextContent("«Туннель не работает 5 минут» не отправлено");
    expect(alert).toHaveTextContent("535 5.7.8 auth failed");
  });

  it("говорит об отказе пробного письма", async () => {
    current = { ...base, channel: "failing", verified: false, test: { state: "failed", error: "no such host", at_utc: "2026-09-14T02:00:00Z" } };
    render(<MailFailureBanner />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Пробное письмо не ушло");
  });

  it("говорит, что после восстановления нужен пароль", async () => {
    current = { ...base, password_set: false, channel: "password_missing" };
    render(<MailFailureBanner />);
    expect(await screen.findByRole("alert")).toHaveTextContent("ввести пароль почты");
  });

  it("ведёт в настройки и уходит, когда письма снова доходят", async () => {
    const user = userEvent.setup();
    current = {
      ...base,
      channel: "failing",
      last_failure: { subject: "Туннель не работает 5 минут", error: "535", at_utc: "2026-09-14T02:00:00Z" },
    };
    render(<MailFailureBanner />);
    await user.click(await screen.findByRole("button", { name: "Открыть настройки" }));
    expect(await screen.findByRole("dialog", { name: "Уведомления" })).toBeInTheDocument();

    current = { ...base, channel: "ok" };
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  });
});

import { defineConfig } from "@playwright/test";

// Две панели, а не одна: обычный экран — это список клиентов, а экран первого
// запуска показывается только когда клиентов нет. Раньше фикстура поднимала
// панель без клиентов, и половина проверок описывала состояние, в котором
// панель никогда не бывает у живого владельца (amnezia-vpn-server-e72j).
export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  use: {
    viewport: { width: 1280, height: 720 },
  },
  projects: [
    {
      name: "panel",
      testMatch: /panel\.spec\.ts/,
      use: { baseURL: "http://127.0.0.1:18787" },
    },
    {
      name: "first-launch",
      testMatch: /first-launch\.spec\.ts/,
      use: { baseURL: "http://127.0.0.1:18788" },
    },
  ],
  webServer: [
    {
      command: "bash e2e/run-panel.sh",
      url: "http://127.0.0.1:18787/login",
      timeout: 120_000,
      reuseExistingServer: false,
    },
    {
      command: "AMNEZIA_E2E_PORT=18788 AMNEZIA_E2E_NO_CLIENTS=1 bash e2e/run-panel.sh",
      url: "http://127.0.0.1:18788/login",
      timeout: 120_000,
      reuseExistingServer: false,
    },
  ],
});

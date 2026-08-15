import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  use: {
    baseURL: "http://127.0.0.1:18787",
    viewport: { width: 1280, height: 720 },
  },
  webServer: {
    command: "bash e2e/run-panel.sh",
    url: "http://127.0.0.1:18787/login",
    timeout: 120_000,
    reuseExistingServer: false,
  },
});

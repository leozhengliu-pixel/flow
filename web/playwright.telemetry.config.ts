import { defineConfig, devices } from '@playwright/test'

const api = 'http://127.0.0.1:4480'
const web = 'http://127.0.0.1:4473'

export default defineConfig({
  testDir: './e2e/telemetry',
  outputDir: 'test-results/telemetry',
  timeout: 60_000,
  workers: 1,
  reporter: 'list',
  use: { baseURL: web, ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 }, trace: 'retain-on-failure' },
  webServer: [
    {
      command: 'node e2e/telemetry/servers.mjs',
      url: `${api}/api/health`,
      timeout: 120_000,
      reuseExistingServer: false,
      gracefulShutdown: { signal: 'SIGTERM', timeout: 8_000 },
    },
    {
      command: 'npm run dev -- --host 127.0.0.1 --port 4473 --strictPort',
      env: { VITE_API_PROXY_TARGET: api },
      url: web,
      timeout: 120_000,
      reuseExistingServer: false,
    },
  ],
})

import { defineConfig, devices } from '@playwright/test'

// Scratch API and Vite for the inbox stale-cache regression spec. It keeps its
// own database and ports so it never races another agent on the shared E2E
// servers.
export default defineConfig({
  testDir: './e2e',
  testMatch: '**/inbox-stale-cache.spec.ts',
  timeout: 60_000,
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: 'list',
  use: {
    baseURL: 'http://127.0.0.1:4175',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],
  webServer: [
    {
      command: "sh -c 'mkdir -p /tmp/oc-inbox-stale && rm -f /tmp/oc-inbox-stale/flow.db /tmp/oc-inbox-stale/flow.db-shm /tmp/oc-inbox-stale/flow.db-wal && cd ../api && FLOW_AUTH_DISABLED=true FLOW_DATABASE_PATH=/tmp/oc-inbox-stale/flow.db FLOW_HTTP_ADDR=127.0.0.1:4181 ${FLOW_E2E_API_BIN:-go run ./cmd/server} >> /tmp/codex-oc-inbox-stale/api.log 2>&1'",
      url: 'http://127.0.0.1:4181/api/health',
      timeout: 120_000,
      reuseExistingServer: false,
    },
    {
      command: 'VITE_API_PROXY_TARGET=http://127.0.0.1:4181 npm run dev -- --host 127.0.0.1 --port 4175',
      url: 'http://127.0.0.1:4175',
      timeout: 120_000,
      reuseExistingServer: false,
    },
  ],
})

import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? [['github'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: 'http://127.0.0.1:4173',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'], ...(process.env.CI ? {} : { channel: 'chrome' }) } },
    { name: 'mobile-chromium', use: { ...devices['Pixel 7'], ...(process.env.CI ? {} : { channel: 'chrome' }) } },
  ],
  webServer: [
    {
      command: 'node e2e/agent-provider.mjs',
      url: 'http://127.0.0.1:4190/health',
      timeout: 30_000,
      reuseExistingServer: !process.env.CI,
    },
    {
      command: "sh -c 'rm -f /tmp/flow-e2e.db && cd ../api && FLOW_AUTH_DISABLED=true FLOW_DATABASE_PATH=/tmp/flow-e2e.db FLOW_HTTP_ADDR=127.0.0.1:4180 FLOW_AGENT_ENABLED=true FLOW_AGENT_PROTOCOL=openai-responses FLOW_AGENT_BASE_URL=http://127.0.0.1:4190 FLOW_AGENT_MODEL=e2e-model FLOW_AGENT_TOOLS_ENABLED=true go run ./cmd/server'",
      url: 'http://127.0.0.1:4180/api/health',
      timeout: 120_000,
      reuseExistingServer: !process.env.CI,
    },
    {
      command: 'VITE_API_PROXY_TARGET=http://127.0.0.1:4180 npm run dev -- --host 127.0.0.1 --port 4173',
      url: 'http://127.0.0.1:4173',
      timeout: 120_000,
      reuseExistingServer: !process.env.CI,
    },
  ],
})

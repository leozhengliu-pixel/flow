import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  // The security specs start their own servers and run from playwright.security.config.ts.
  testIgnore: '**/security/**',
  timeout: 30_000,
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? [['list'], ['github'], ['html', { open: 'never' }]] : 'list',
  reportSlowTests: { max: 10, threshold: 0 },
  use: {
    baseURL: 'http://127.0.0.1:4173',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    // Phone specs select their own viewports/touch settings, including their
    // desktop-width regressions. Running them in both projects repeats them.
    { name: 'chromium', testIgnore: ['**/security/**', '**/mobile-*.spec.ts'], use: { ...devices['Desktop Chrome'], ...(process.env.CI ? {} : { channel: 'chrome' }) } },
    // These files already skip phones (or explicitly force desktop settings).
    // Exclude them before sharding so skipped tests do not skew shard sizes.
    { name: 'mobile-chromium', testIgnore: ['**/security/**', '**/desktop-*.spec.ts', '**/keyboard-shortcuts.spec.ts', '**/loops-output-contract.spec.ts'], use: { ...devices['Pixel 7'], ...(process.env.CI ? {} : { channel: 'chrome' }) } },
  ],
  webServer: [
    {
      command: 'node e2e/agent-provider.mjs',
      url: 'http://127.0.0.1:4190/health',
      timeout: 30_000,
      reuseExistingServer: !process.env.CI,
    },
    {
      command: "sh -c 'rm -f \"$FLOW_E2E_DATABASE_PATH\" && cd ../api && FLOW_AUTH_DISABLED=true FLOW_DATABASE_PATH=\"$FLOW_E2E_DATABASE_PATH\" FLOW_HTTP_ADDR=127.0.0.1:4180 FLOW_AGENT_ENABLED=true FLOW_AGENT_PROTOCOL=openai-responses FLOW_AGENT_BASE_URL=http://127.0.0.1:4190 FLOW_AGENT_MODEL=e2e-model FLOW_AGENT_TOOLS_ENABLED=true FLOW_AGENT_WRITE_TOOLS=true ${FLOW_E2E_API_BIN:-go run ./cmd/server} >> \"${FLOW_E2E_API_LOG:-/dev/stderr}\" 2>&1'",
      env: { FLOW_E2E_DATABASE_PATH: process.env.FLOW_E2E_DATABASE_PATH ?? '/tmp/flow-e2e.db' },
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

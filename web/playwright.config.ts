import { defineConfig, devices, type PlaywrightTestConfig } from '@playwright/test'

export const desktopPerformanceSpecs = ['**/desktop-timeline-performance.spec.ts', '**/desktop-document-first-paint.spec.ts']
const standaloneSpecs = ['**/security/**', '**/local-store/**', ...desktopPerformanceSpecs]

export function browserWebServers(
  ports: { web: number; api: number; provider: number },
  mode: 'dev' | 'preview',
): PlaywrightTestConfig['webServer'] {
  const reuseExistingServer = mode === 'dev' && !process.env.CI
  const apiAddress = `127.0.0.1:${ports.api}`
  const providerURL = `http://127.0.0.1:${ports.provider}`
  const databasePath = mode === 'preview' ? '/tmp/flow-e2e-performance.db' : '/tmp/flow-e2e.db'
  return [
    {
      command: 'node e2e/agent-provider.mjs',
      env: { FLOW_AGENT_PROVIDER_PORT: String(ports.provider) },
      url: `${providerURL}/health`,
      timeout: 30_000,
      reuseExistingServer,
    },
    {
      command: "sh -c 'rm -f \"$FLOW_E2E_DATABASE_PATH\" && cd ../api && FLOW_DATABASE_PATH=\"$FLOW_E2E_DATABASE_PATH\" ${FLOW_E2E_API_BIN:-go run ./cmd/server} >> \"${FLOW_E2E_API_LOG:-/dev/stderr}\" 2>&1'",
      env: {
        FLOW_E2E_DATABASE_PATH: process.env.FLOW_E2E_DATABASE_PATH ?? databasePath,
        FLOW_AUTH_DISABLED: 'true',
        FLOW_HTTP_ADDR: apiAddress,
        FLOW_AGENT_ENABLED: 'true',
        FLOW_AGENT_PROTOCOL: 'openai-responses',
        FLOW_AGENT_BASE_URL: providerURL,
        FLOW_AGENT_MODEL: 'e2e-model',
        FLOW_AGENT_TOOLS_ENABLED: 'true',
        FLOW_AGENT_WRITE_TOOLS: 'true',
      },
      url: `http://${apiAddress}/api/health`,
      timeout: 120_000,
      reuseExistingServer,
    },
    {
      command: `npm run ${mode} -- --host 127.0.0.1 --port ${ports.web} --strictPort`,
      env: { VITE_API_PROXY_TARGET: `http://${apiAddress}` },
      url: `http://127.0.0.1:${ports.web}`,
      timeout: 120_000,
      reuseExistingServer,
    },
  ]
}

export default defineConfig({
  testDir: './e2e',
  testIgnore: standaloneSpecs,
  timeout: 30_000,
  // CI serves the app from the Vite dev server: every page.goto refetches about
  // 1,100 modules, which takes 4-5 s on a loaded hosted runner before any data.
  expect: { timeout: process.env.CI ? 10_000 : 5_000 },
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
    { name: 'chromium', testIgnore: [...standaloneSpecs, '**/mobile-*.spec.ts'], use: { ...devices['Desktop Chrome'], ...(process.env.CI ? {} : { channel: 'chrome' }) } },
    // These files already skip phones (or explicitly force desktop settings).
    // Exclude them before sharding so skipped tests do not skew shard sizes.
    { name: 'mobile-chromium', testIgnore: [...standaloneSpecs, '**/desktop-*.spec.ts', '**/keyboard-shortcuts.spec.ts', '**/loops-output-contract.spec.ts'], use: { ...devices['Pixel 7'], ...(process.env.CI ? {} : { channel: 'chrome' }) } },
  ],
  webServer: browserWebServers({ web: 4173, api: 4180, provider: 4190 }, 'dev'),
})

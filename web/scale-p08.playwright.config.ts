import { defineConfig, devices } from '@playwright/test'
import base from './playwright.config'

const webPort = Number(process.env.SCALE_P08_WEB_PORT ?? 42831)
const apiPort = Number(process.env.SCALE_P08_API_PORT ?? 42832)
const providerPort = Number(process.env.SCALE_P08_PROVIDER_PORT ?? 42833)
const artifacts = process.env.SCALE_P08_ARTIFACTS ?? '/tmp/codex-scale-p08'

export default defineConfig({
  ...base,
  testMatch: 'scale-p08-*.spec.ts',
  timeout: 90_000,
  retries: 0,
  outputDir: `${artifacts}/browser-results`,
  use: { ...base.use, baseURL: `http://127.0.0.1:${webPort}` },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } },
    { name: 'phone', use: { ...devices['Pixel 7'], viewport: { width: 390, height: 844 }, colorScheme: 'dark' } },
  ],
  webServer: [
    { command: 'node e2e/agent-provider.mjs', env: { FLOW_AGENT_PROVIDER_PORT: String(providerPort) }, url: `http://127.0.0.1:${providerPort}/health`, reuseExistingServer: false },
    {
      command: process.env.FLOW_E2E_API_BIN ?? `${artifacts}/flow-api`,
      cwd: '../api',
      env: {
        FLOW_AUTH_DISABLED: 'true', FLOW_WORKSPACE_TUTORIAL: 'off',
        FLOW_DATABASE_PATH: `${artifacts}/browser.db`, FLOW_STORAGE_LOCAL_PATH: `${artifacts}/uploads`,
        FLOW_HTTP_ADDR: `127.0.0.1:${apiPort}`, FLOW_APP_URL: `http://127.0.0.1:${webPort}`,
        FLOW_AGENT_ENABLED: 'true', FLOW_AGENT_PROTOCOL: 'openai-responses',
        FLOW_AGENT_BASE_URL: `http://127.0.0.1:${providerPort}`, FLOW_AGENT_MODEL: 'e2e-model',
        FLOW_AGENT_TOOLS_ENABLED: 'true', FLOW_AGENT_WRITE_TOOLS: 'true',
      },
      url: `http://127.0.0.1:${apiPort}/api/health`, timeout: 120_000, reuseExistingServer: false,
    },
    {
      command: `npm run dev -- --host 127.0.0.1 --port ${webPort} --strictPort`,
      env: { VITE_API_PROXY_TARGET: `http://127.0.0.1:${apiPort}`, VITE_APPLIED_REALTIME_CURSOR: 'true', VITE_BOUNDED_BROWSER_CACHE: 'true', VITE_PAGED_ISSUES: 'true' },
      url: `http://127.0.0.1:${webPort}`, timeout: 120_000, reuseExistingServer: false,
    },
  ],
})

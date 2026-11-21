import { defineConfig, devices } from '@playwright/test'
import { mkdirSync, mkdtempSync } from 'node:fs'
import { join } from 'node:path'

const artifacts = process.env.FLOW_LOCAL_STORE_ARTIFACTS ?? '/tmp/codex-web-local-first'
mkdirSync(artifacts, { recursive: true })
const scratch = mkdtempSync(join(artifacts, 'e2e-'))
const webPort = Number(process.env.FLOW_LOCAL_STORE_WEB_PORT ?? 43371)
const apiPort = Number(process.env.FLOW_LOCAL_STORE_API_PORT ?? 43380)
const apiURL = `http://127.0.0.1:${apiPort}`
const dist = process.env.FLOW_LOCAL_STORE_WEB_DIST
const webURL = dist ? apiURL : `http://127.0.0.1:${webPort}`
const engines = ['chromium', 'webkit', 'firefox'] as const
const realAuth = process.env.FLOW_LOCAL_STORE_REAL_AUTH === '1'
const spec = process.env.FLOW_LOCAL_STORE_SPEC ?? (dist ? 'shell.spec.ts' : realAuth ? 'authority.spec.ts' : 'shadow.spec.ts')
const visualResume = Boolean(process.env.FLOW_LOCAL_STORE_SHOTS && spec === 'resume.spec.ts')
const providerPort = Number(process.env.FLOW_LOCAL_STORE_PROVIDER_PORT ?? 43390)
const providerURL = `http://127.0.0.1:${providerPort}`
const allowed = (tests: string[]) => engines.flatMap(engine => tests.map(test => `browser-local-store-${engine}-${test}`)).join(',')
const partialWorkspaces = ['partials', 'partials-authority', 'partials-budget', 'partials-other']
const outboxWorkspaces = ['outbox', 'outbox-fallback', 'outbox-account', 'outbox-revoked']
const apiServer = {
  command: 'cd ../api && exec ${FLOW_E2E_API_BIN:-go run -p 1 ./cmd/server}',
  url: `${apiURL}/api/health`, timeout: 180_000, reuseExistingServer: false,
  env: {
    FLOW_AUTH_DISABLED: realAuth ? 'false' : 'true', FLOW_DEV_AUTH_TOKENS: 'true', FLOW_APP_URL: webURL,
    FLOW_DATABASE_DRIVER: 'sqlite', FLOW_DATABASE_PATH: join(scratch, 'flow.db'),
    FLOW_STORAGE_LOCAL_PATH: join(scratch, 'uploads'), FLOW_HTTP_ADDR: `127.0.0.1:${apiPort}`,
    TMPDIR: scratch,
    FLOW_SYNC_V2: 'on', FLOW_RECORD_JOURNAL: 'on', FLOW_SYNC_MANIFEST: 'on', FLOW_INDEXED_READS: 'on',
    FLOW_BOUNDED_METADATA: process.env.FLOW_BOUNDED_METADATA ?? 'off',
    FLOW_LOCAL_STORE_WORKSPACES: allowed(['verify', 'storage', 'count', 'authority', 'shell', 'resume', 'resume-authority', 'kill-switch', 'heap', ...partialWorkspaces, ...outboxWorkspaces]),
    FLOW_LOCAL_STORE_RESUME_WORKSPACES: allowed(['resume', 'resume-authority', 'heap', ...partialWorkspaces, ...outboxWorkspaces]),
    ...(dist ? { FLOW_STATIC_PATH: dist, FLOW_SECURITY_CSP: 'enforce' } : {}),
    ...(visualResume ? { FLOW_AGENT_ENABLED: 'true', FLOW_AGENT_PROTOCOL: 'openai-responses', FLOW_AGENT_BASE_URL: providerURL, FLOW_AGENT_MODEL: 'e2e-model' } : {}),
    GOMAXPROCS: '2',
  },
}
const servers = [
  ...(visualResume ? [{ command: 'node e2e/agent-provider.mjs', url: `${providerURL}/health`, reuseExistingServer: false,
    env: { FLOW_AGENT_PROVIDER_PORT: String(providerPort) } }] : []),
  apiServer,
]

export default defineConfig({
  testDir: './e2e/local-store',
  testMatch: spec,
  timeout: 120_000,
  workers: 1,
  retries: 0,
  outputDir: join(scratch, 'results'),
  reporter: 'list',
  use: { baseURL: webURL, timezoneId: 'UTC', trace: process.env.FLOW_LOCAL_STORE_TRACE === '1' ? 'retain-on-failure' : 'off' },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
    { name: 'webkit', use: { ...devices['Desktop Safari'] } },
    { name: 'firefox', use: { ...devices['Desktop Firefox'], ...(spec === 'heap.spec.ts' ? { launchOptions: {
      args: ['--start-debugger-server', join(artifacts, 'ff.sock')],
      firefoxUserPrefs: { 'devtools.debugger.remote-enabled': true, 'devtools.debugger.prompt-connection': false },
    } } : {}) } },
  ],
  webServer: dist ? servers : [
    ...servers,
    {
      command: `npm run dev -- --host 127.0.0.1 --port ${webPort} --strictPort`,
      url: webURL, timeout: 120_000, reuseExistingServer: false,
      env: { VITE_LOCAL_STORE: process.env.FLOW_LOCAL_STORE_CLIENT === 'off' ? 'false' : 'true', VITE_SYNC_V2: 'true', VITE_PAGED_ISSUES: 'false', VITE_API_PROXY_TARGET: apiURL },
    },
  ],
})

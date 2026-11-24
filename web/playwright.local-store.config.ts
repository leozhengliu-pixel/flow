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
const baselineDist = process.env.FLOW_LOCAL_STORE_BASELINE_WEB_DIST
const baselineBinary = process.env.FLOW_LOCAL_STORE_BASELINE_API_BIN
const baselineURL = `http://127.0.0.1:${Number(process.env.FLOW_LOCAL_STORE_BASELINE_API_PORT ?? 43381)}`
const webURL = dist ? apiURL : `http://127.0.0.1:${webPort}`
const realAuth = process.env.FLOW_LOCAL_STORE_REAL_AUTH === '1'
const specs = (process.env.FLOW_LOCAL_STORE_SPEC ?? (dist ? 'shell.spec.ts' : realAuth ? 'authority.spec.ts' : 'shadow.spec.ts')).split(',')
const pairedTiming = specs.includes('timing.spec.ts')
const visualResume = Boolean(process.env.FLOW_LOCAL_STORE_SHOTS && specs.includes('resume.spec.ts'))
const providerPort = Number(process.env.FLOW_LOCAL_STORE_PROVIDER_PORT ?? 43390)
const providerURL = `http://127.0.0.1:${providerPort}`

if (pairedTiming && !(dist && baselineDist && baselineBinary)) {
  throw new Error('Paired timing requires FLOW_LOCAL_STORE_WEB_DIST, FLOW_LOCAL_STORE_BASELINE_WEB_DIST and FLOW_LOCAL_STORE_BASELINE_API_BIN')
}

const apiServer = {
  command: 'cd ../api && exec ${FLOW_E2E_API_BIN:-go run -p 1 ./cmd/server}',
  url: `${apiURL}/api/health`, timeout: 180_000, reuseExistingServer: false,
  env: {
    FLOW_AUTH_DISABLED: realAuth ? 'false' : 'true', FLOW_DEV_AUTH_TOKENS: 'true', FLOW_APP_URL: webURL,
    FLOW_DATABASE_DRIVER: 'sqlite', FLOW_DATABASE_PATH: join(scratch, 'flow.db'),
    FLOW_STORAGE_LOCAL_PATH: join(scratch, 'uploads'), FLOW_HTTP_ADDR: `127.0.0.1:${apiPort}`,
    TMPDIR: scratch,
    FLOW_SYNC_V2: 'on', FLOW_RECORD_HYDRATION: 'on', FLOW_RECORD_JOURNAL: 'on', FLOW_SYNC_MANIFEST: 'on', FLOW_INDEXED_READS: 'on',
    FLOW_BOUNDED_METADATA: process.env.FLOW_BOUNDED_METADATA ?? 'off',
    ...(dist ? { FLOW_STATIC_PATH: dist, FLOW_SECURITY_CSP: 'enforce' } : {}),
    ...(visualResume ? { FLOW_AGENT_ENABLED: 'true', FLOW_AGENT_PROTOCOL: 'openai-responses', FLOW_AGENT_BASE_URL: providerURL, FLOW_AGENT_MODEL: 'e2e-model' } : {}),
    GOMAXPROCS: process.env.GOMAXPROCS ?? '2',
  },
}
const servers = [
  ...(visualResume ? [{ command: 'node e2e/agent-provider.mjs', url: `${providerURL}/health`, reuseExistingServer: false,
    env: { FLOW_AGENT_PROVIDER_PORT: String(providerPort) } }] : []),
  apiServer,
  ...(pairedTiming ? [{
    ...apiServer, command: 'cd ../api && exec "$FLOW_LOCAL_STORE_BASELINE_API_BIN"', url: `${baselineURL}/api/health`,
    env: {
      ...apiServer.env, FLOW_APP_URL: baselineURL, FLOW_HTTP_ADDR: new URL(baselineURL).host,
      FLOW_DATABASE_PATH: join(scratch, 'baseline.db'), FLOW_STORAGE_LOCAL_PATH: join(scratch, 'baseline-uploads'),
      FLOW_STATIC_PATH: baselineDist!, FLOW_LOCAL_STORE_WORKSPACES: '', FLOW_LOCAL_STORE_RESUME_WORKSPACES: '',
    },
  }] : []),
]

export default defineConfig({
  testDir: './e2e/local-store',
  testMatch: specs,
  timeout: 120_000,
  workers: 1,
  retries: 0,
  outputDir: join(scratch, 'results'),
  reporter: 'list',
  use: { baseURL: webURL, timezoneId: 'UTC', trace: process.env.FLOW_LOCAL_STORE_TRACE === '1' ? 'retain-on-failure' : 'off' },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
    { name: 'webkit', use: { ...devices['Desktop Safari'] } },
    { name: 'firefox', use: { ...devices['Desktop Firefox'], ...(specs.includes('heap.spec.ts') ? { launchOptions: {
      args: ['--start-debugger-server', join(artifacts, 'ff.sock')],
      firefoxUserPrefs: { 'devtools.debugger.remote-enabled': true, 'devtools.debugger.prompt-connection': false },
    } } : {}) } },
  ],
  webServer: dist ? servers : [
    ...servers,
    {
      command: `npm run dev -- --host 127.0.0.1 --port ${webPort} --strictPort`,
      url: webURL, timeout: 120_000, reuseExistingServer: false,
      env: { VITE_PAGED_ISSUES: 'false', VITE_API_PROXY_TARGET: apiURL },
    },
  ],
})

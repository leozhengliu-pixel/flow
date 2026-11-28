import { spawn } from 'node:child_process'
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:http'
import { join } from 'node:path'

const root = '/tmp/codex-telemetry'
mkdirSync(root, { recursive: true })
const dir = mkdtempSync(join(root, 'browser-'))
const config = join(dir, 'config.yaml')
const address = '127.0.0.1:4481'
writeFileSync(config, JSON.stringify({
  server: {
    telemetry: {
      enabled: true,
      endpoint: `http://${address}`,
      interval: '1s',
      sampleRate: 1,
      frontend: { enabled: true, sampleRate: 1 },
      diagnostics: { snapshotDir: '' },
    },
  },
}))

let browserErrors = 0
let leaked = false
const collector = createServer(async (req, res) => {
  if (req.url === '/observed') {
    res.setHeader('Content-Type', 'application/json')
    res.end(JSON.stringify({ browserErrors, leaked }))
    return
  }
  let data = Buffer.alloc(0)
  for await (const piece of req) {
    if (data.length + piece.length <= 4 * 1024 * 1024) data = Buffer.concat([data, piece])
  }
  if (req.url === '/v1/logs' && data.includes('flow.browser.js_error')) browserErrors++
  leaked ||= Boolean(req.headers.cookie) || data.includes('telemetry-fixture-email@example.test') || data.includes('telemetry-fixture-token')
  res.end()
})
await new Promise(resolve => collector.listen(4481, '127.0.0.1', resolve))

const bin = process.env.FLOW_E2E_API_BIN
const api = spawn(bin ?? 'go', bin ? ['--config', config] : ['run', './cmd/server', '--config', config], {
  cwd: new URL('../../../api/', import.meta.url),
  stdio: 'inherit',
  env: {
    ...process.env,
    FLOW_AUTH_DISABLED: 'false',
    FLOW_DEV_AUTH_TOKENS: 'true',
    FLOW_DATABASE_DRIVER: 'sqlite',
    FLOW_DATABASE_PATH: join(dir, 'flow.db'),
    FLOW_STORAGE_DRIVER: 'local',
    FLOW_STORAGE_LOCAL_PATH: join(dir, 'uploads'),
    FLOW_REDIS_MODE: 'disabled',
    FLOW_REDIS_CACHE: 'off',
    FLOW_AGENT_ENABLED: 'false',
    FLOW_APP_URL: 'http://127.0.0.1:4473',
    FLOW_HTTP_ADDR: '127.0.0.1:4480',
  },
})
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => api.kill('SIGTERM'))
api.on('exit', code => {
  collector.close()
  rmSync(dir, { recursive: true, force: true })
  process.exitCode = code ?? 0
})

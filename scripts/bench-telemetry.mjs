import { execFileSync, spawn } from 'node:child_process'
import { once } from 'node:events'
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:http'
import { join } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'

const [bin, iterations = '360'] = process.argv.slice(2)
const count = Number(iterations)
if (!bin || !Number.isInteger(count) || count <= 0) {
  throw new Error('Usage: node scripts/bench-telemetry.mjs /tmp/codex-telemetry/flow-api [iterations]')
}
const root = '/tmp/codex-telemetry'
const workspace = 'telemetry-bench'
mkdirSync(root, { recursive: true })
const collector = createServer((req, res) => {
  req.resume()
  req.on('end', () => res.end())
})
collector.listen(0, '127.0.0.1')
await once(collector, 'listening')
const endpoint = `http://127.0.0.1:${collector.address().port}`

async function freePort() {
  const socket = createServer().listen(0, '127.0.0.1')
  await once(socket, 'listening')
  const { port } = socket.address()
  await new Promise(resolve => socket.close(resolve))
  return port
}

function processUsage(pid) {
  const [time, rss] = execFileSync('ps', ['-p', String(pid), '-o', 'time=', '-o', 'rss='], { encoding: 'utf8' }).trim().split(/\s+/)
  return { cpu: time.split(':').reduce((total, part) => total * 60 + Number(part), 0), rssMiB: Number(rss) / 1024 }
}

const percentile = (values, p) => [...values].sort((a, b) => a - b)[Math.ceil(values.length * p) - 1]
const summary = values => ({ p50: percentile(values, .5).toFixed(2), p95: percentile(values, .95).toFixed(2), p99: percentile(values, .99).toFixed(2) })

async function bench(rate, round) {
  const dir = mkdtempSync(join(root, 'bench-'))
  const config = join(dir, 'config.yaml')
  const port = await freePort()
  const base = `http://127.0.0.1:${port}`
  writeFileSync(config, JSON.stringify({ server: { telemetry: {
    enabled: rate > 0, endpoint, sampleRate: rate, interval: '1s', diagnostics: { snapshotDir: '' },
  } } }))
  const child = spawn(bin, ['--config', config], {
    stdio: 'ignore',
    env: {
      ...process.env,
      GOMAXPROCS: '2',
      GOMEMLIMIT: '512MiB',
      FLOW_AUTH_DISABLED: 'true',
      FLOW_DATABASE_DRIVER: 'sqlite',
      FLOW_DATABASE_PATH: join(dir, 'flow.db'),
      FLOW_STORAGE_DRIVER: 'local',
      FLOW_STORAGE_LOCAL_PATH: join(dir, 'uploads'),
      FLOW_REDIS_MODE: 'disabled',
      FLOW_REDIS_CACHE: 'off',
      FLOW_AGENT_ENABLED: 'false',
      FLOW_APP_URL: base,
      FLOW_HTTP_ADDR: `127.0.0.1:${port}`,
      FLOW_LOG_LEVEL: 'error',
    },
  })
  const stop = once(child, 'exit')
  async function call(method, path, body, expected = 200) {
    const response = await fetch(base + path, {
      method,
      headers: { 'Content-Type': 'application/json', 'X-Workspace-Key': workspace },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(5000),
    })
    if (response.status !== expected) throw new Error(`${method} ${path.split('?')[0]} expected ${expected}, got ${response.status}`)
    return response.json()
  }
  try {
    const started = Date.now()
    for (;;) {
      const ready = await fetch(`${base}/api/health`, { signal: AbortSignal.timeout(500) }).catch(() => undefined)
      if (ready?.ok) break
      if (child.exitCode !== null || Date.now() - started > 30_000) throw new Error('Scratch API did not start')
      await delay(100)
    }
    await call('POST', '/api/workspaces', { name: 'Telemetry Benchmark', urlKey: workspace, region: 'us', tutorial: false }, 201)
    const data = await call('GET', '/api/bootstrap')
    const team = data.teams?.[0] ?? await call('POST', '/api/teams', { name: 'Benchmark', key: 'BENCH' }, 201)
    let issue
    for (let i = 0; i < 50; i++) {
      issue = await call('POST', '/api/issues', { title: `Fixture issue ${i}`, teamId: team.id }, 201)
    }
    const doc = await call('POST', '/api/documents', { title: 'Benchmark' }, 201)
    const elapsed = [[], [], []]
    const ops = [
      () => call('GET', '/api/issues?limit=50'),
      i => call('PATCH', `/api/issues/${issue.id}`, { title: `Changed fixture ${i}` }),
      i => {
        const text = `Updated fixture ${i}: ${'text '.repeat(900)}`
        return call('PATCH', `/api/documents/${doc.id}`, {
          content: text,
          contentData: { type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text }] }] },
        })
      },
    ]
    for (let i = 0; i < 20; i++) {
      for (const op of ops) await op(-i)
    }
    const before = processUsage(child.pid)
    let maxRSS = before.rssMiB
    for (let i = 0; i < count; i++) {
      const position = i % ops.length
      const start = performance.now()
      await ops[position](i)
      elapsed[position].push(performance.now() - start)
      if (i % 15 === 0) maxRSS = Math.max(maxRSS, processUsage(child.pid).rssMiB)
    }
    const after = processUsage(child.pid)
    console.log(JSON.stringify({
      round, rate, requests: count, latencyMs: elapsed.map(summary),
      processCPUSeconds: (after.cpu - before.cpu).toFixed(2), peakSampledRSSMiB: Math.max(maxRSS, after.rssMiB).toFixed(1),
    }))
  } finally {
    child.kill('SIGTERM')
    await stop
    rmSync(dir, { recursive: true, force: true })
  }
}

try {
  for (let round = 0; round < 3; round++) {
    const rates = [0, .1, 1]
    for (let i = 0; i < rates.length; i++) await bench(rates[(round + i) % rates.length], round + 1)
  }
} finally {
  collector.close()
}

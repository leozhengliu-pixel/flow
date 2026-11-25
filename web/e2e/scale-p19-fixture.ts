import { spawn, spawnSync, type ChildProcess } from 'node:child_process'
import { createWriteStream, existsSync, mkdirSync, mkdtempSync } from 'node:fs'
import net from 'node:net'
import path from 'node:path'
import { randomBytes } from 'node:crypto'
import { createServer } from 'vite'
import { test as workspaceTest } from './workspace-fixture'

async function freePort() {
  const server = net.createServer()
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const port = (server.address() as net.AddressInfo).port
  await new Promise<void>(resolve => server.close(() => resolve()))
  return port
}

async function stop(child: ChildProcess) {
  if (child.exitCode !== null || child.signalCode !== null) return
  await new Promise<void>(resolve => {
    const timer = setTimeout(() => child.kill('SIGKILL'), 5000)
    child.once('exit', () => { clearTimeout(timer); resolve() })
    child.kill('SIGTERM')
  })
}

export const test = workspaceTest.extend<{ scaleP19Enabled: boolean; scaleP19ServerEnabled: boolean; scaleP19URL: string }>({
  scaleP19Enabled: [false, { option: true }],
  scaleP19ServerEnabled: [false, { option: true }],
  scaleP19URL: [async ({ scaleP19Enabled, scaleP19ServerEnabled }, provide) => {
    const web = path.resolve(import.meta.dirname, '..')
    const root = process.env.FLOW_SCALE_P19_ARTIFACTS ?? '/tmp/codex-scale-p19'
    mkdirSync(root, { recursive: true })
    const scratch = mkdtempSync(path.join(root, 'browser-'))
    let binary = process.env.FLOW_E2E_API_BIN
    if (!binary || !existsSync(binary)) {
      binary = path.join(scratch, 'flow-api')
      const built = spawnSync('go', ['build', '-p', '1', '-o', binary, './cmd/server'], { cwd: path.join(web, '../api'), stdio: 'inherit', env: { ...process.env, GOMAXPROCS: '2', GOMEMLIMIT: '1536MiB' } })
      if (built.status !== 0) throw new Error('Scratch API build failed')
    }
    const port = await freePort()
    const log = createWriteStream(path.join(scratch, 'api.log'))
    const sync = scaleP19ServerEnabled ? 'on' : 'off'
    const api = spawn(binary, [], { cwd: path.join(web, '../api'), env: { ...process.env, GOMAXPROCS: '2', GOMEMLIMIT: '1536MiB', FLOW_AUTH_DISABLED: 'true', FLOW_SYNC_MANIFEST: sync, FLOW_INDEXED_READS: sync, FLOW_RECORD_JOURNAL: sync, FLOW_SYNC_V2: sync, FLOW_CONNECTOR_SECRET_KEY: randomBytes(32).toString('base64'), FLOW_DATABASE_PATH: path.join(scratch, 'flow.db'), FLOW_HTTP_ADDR: `127.0.0.1:${port}` }, stdio: ['ignore', 'pipe', 'pipe'] })
    api.stdout!.pipe(log); api.stderr!.pipe(log)
    const flag = JSON.stringify(String(scaleP19Enabled))
    const vite = await createServer({ root: web, configFile: path.join(web, 'vite.config.ts'), logLevel: 'error', define: { 'import.meta.env.VITE_SYNC_V2': flag, 'import.meta.env.VITE_PROJECT_QUERY_WINDOWS': flag, 'import.meta.env.VITE_INITIATIVE_QUERY_WINDOWS': flag }, server: { host: '127.0.0.1', port: 0, proxy: { '/api': { target: `http://127.0.0.1:${port}`, ws: true } } } })
    try {
      const deadline = Date.now() + 60_000
      let ready = false
      while (Date.now() < deadline) {
        if (api.exitCode !== null) throw new Error(`Scratch API exited: ${api.exitCode}`)
        try { if ((await fetch(`http://127.0.0.1:${port}/api/health`)).ok) { ready = true; break } } catch { /* Listener is starting. */ }
        await new Promise(resolve => setTimeout(resolve, 100))
      }
      if (!ready) throw new Error('Scratch API did not start')
      await vite.listen()
      await provide(`http://127.0.0.1:${(vite.httpServer!.address() as net.AddressInfo).port}`)
    } finally {
      await vite.close()
      await stop(api)
      log.end()
    }
  }, { timeout: 120_000 }],
  baseURL: async ({ scaleP19URL }, provide) => { await provide(scaleP19URL) },
})

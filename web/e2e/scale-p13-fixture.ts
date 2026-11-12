import { spawn, spawnSync, type ChildProcess } from 'node:child_process'
import { createWriteStream, existsSync, mkdirSync, mkdtempSync } from 'node:fs'
import net from 'node:net'
import path from 'node:path'
import { createServer } from 'vite'
import { test as workspaceTest } from './workspace-fixture'

async function freePort(): Promise<number> {
  const server = net.createServer()
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const port = (server.address() as net.AddressInfo).port
  await new Promise<void>(resolve => server.close(() => resolve()))
  return port
}

async function ready(url: string, child: ChildProcess) {
  const deadline = Date.now() + 30_000
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error(`P13 test server exited: ${child.exitCode}`)
    try { if ((await fetch(url)).ok) return } catch { /* The local listener is still starting. */ }
    await new Promise(resolve => setTimeout(resolve, 100))
  }
  throw new Error(`P13 test server did not start: ${url}`)
}

async function stop(child: ChildProcess) {
  if (child.exitCode !== null || child.signalCode !== null) return
  await new Promise<void>(resolve => {
    const timeout = setTimeout(() => { child.kill('SIGKILL') }, 5000)
    child.once('exit', () => { clearTimeout(timeout); resolve() })
    child.kill('SIGTERM')
  })
}

// A dedicated API/provider/Vite worker makes this spec independent of the main suite's ports and model fixture.
export const test = workspaceTest.extend<object, { scaleP13URL: string }>({
  scaleP13URL: [async ({ browserName }, provide) => {
    const web = path.resolve(import.meta.dirname, '..')
    const root = '/tmp/codex-scale-p13'
    mkdirSync(root, { recursive: true })
    const scratch = mkdtempSync(path.join(root, `browser-${browserName}-`))
    let binary = process.env.FLOW_SECURITY_API_BIN ?? process.env.FLOW_E2E_API_BIN
    if (!binary || !existsSync(binary)) {
      binary = path.join(scratch, 'flow-api')
      const built = spawnSync('go', ['build', '-p', '1', '-o', binary, './cmd/server'], { cwd: path.join(web, '../api'), stdio: 'inherit', env: { ...process.env, GOMAXPROCS: '2', GOMEMLIMIT: '1536MiB' } })
      if (built.status !== 0) throw new Error('P13 API build failed')
    }
    const apiPort = await freePort(), providerPort = await freePort()
    const providerLog = createWriteStream(path.join(scratch, 'provider.log'))
    const apiLog = createWriteStream(path.join(scratch, 'api.log'))
    const provider = spawn(process.execPath, [path.join(web, 'e2e/scale-p13-provider.mjs')], { env: { ...process.env, FLOW_AGENT_PROVIDER_PORT: String(providerPort) }, stdio: ['ignore', 'pipe', 'pipe'] })
    provider.stdout!.pipe(providerLog); provider.stderr!.pipe(providerLog)
    const api = spawn(binary, [], { cwd: path.join(web, '../api'), env: { ...process.env, GOMAXPROCS: '2', GOMEMLIMIT: '1536MiB', FLOW_AUTH_DISABLED: 'true', FLOW_DATABASE_PATH: path.join(scratch, 'flow.db'), FLOW_HTTP_ADDR: `127.0.0.1:${apiPort}`, FLOW_AGENT_ENABLED: 'true', FLOW_AGENT_PROTOCOL: 'openai-responses', FLOW_AGENT_BASE_URL: `http://127.0.0.1:${providerPort}`, FLOW_AGENT_MODEL: 'e2e-model', FLOW_AGENT_TOOLS_ENABLED: 'true', FLOW_AGENT_WRITE_TOOLS: 'true' }, stdio: ['ignore', 'pipe', 'pipe'] })
    api.stdout!.pipe(apiLog); api.stderr!.pipe(apiLog)
    const vite = await createServer({ root: web, configFile: path.join(web, 'vite.config.ts'), logLevel: 'error', define: { 'import.meta.env.VITE_SYNC_V2': '"true"' }, server: { host: '127.0.0.1', port: 0, proxy: { '/api': { target: `http://127.0.0.1:${apiPort}`, ws: true } } } })
    try {
      await ready(`http://127.0.0.1:${providerPort}/health`, provider)
      await ready(`http://127.0.0.1:${apiPort}/api/health`, api)
      await vite.listen()
      await provide(`http://127.0.0.1:${(vite.httpServer!.address() as net.AddressInfo).port}`)
    } finally {
      await vite.close()
      await stop(api)
      await stop(provider)
      apiLog.end(); providerLog.end()
    }
  }, { scope: 'worker' }],
  baseURL: async ({ scaleP13URL }, provide) => { await provide(scaleP13URL) },
})

import { spawn, spawnSync, type ChildProcess } from 'node:child_process'
import { createWriteStream, mkdirSync, mkdtempSync } from 'node:fs'
import net from 'node:net'
import path from 'node:path'
import { createServer, type ViteDevServer } from 'vite'
import type { Socket } from 'node:net'
import { test as workspaceTest } from './workspace-fixture'

const artifacts = process.env.FLOW_SCALE_P20_ARTIFACTS ?? '/tmp/codex-scale-p20-enable'

async function freePort() {
  const server = net.createServer()
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
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

function trackSockets(vite: ViteDevServer) {
  const sockets = new Set<Socket>()
  vite.httpServer?.on('connection', socket => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)) })
  return async () => {
    for (const socket of sockets) socket.destroy()
    await vite.close()
  }
}

export const test = workspaceTest.extend<{ scaleP20Flags: boolean; scaleP20URL: string; scaleP20LegacyURL: string }>({
  scaleP20Flags: [false, { option: true }],
  scaleP20URL: [async ({ scaleP20Flags }, provide) => {
    const web = path.resolve(import.meta.dirname, '..')
    const root = artifacts
    mkdirSync(path.join(root, 'shots'), { recursive: true })
    const scratch = mkdtempSync(path.join(root, 'browser-'))
    const binary = process.env.FLOW_E2E_API_BIN ?? path.join(scratch, 'flow-api')
    if (!process.env.FLOW_E2E_API_BIN) {
      const built = spawnSync('go', ['build', '-p', '1', '-o', binary, './cmd/server'], { cwd: path.join(web, '../api'), stdio: 'inherit', env: { ...process.env, GOMAXPROCS: '2', GOMEMLIMIT: '1536MiB' } })
      if (built.status !== 0) throw new Error('P20 API build failed')
    }
    const apiPort = await freePort()
    const log = createWriteStream(path.join(scratch, 'api.log'))
    const api = spawn(binary, [], { cwd: path.join(web, '../api'), env: { ...process.env, GOMAXPROCS: '4', GOMEMLIMIT: '1536MiB', FLOW_AUTH_DISABLED: 'true', FLOW_DATABASE_PATH: path.join(scratch, 'flow.db'), FLOW_HTTP_ADDR: `127.0.0.1:${apiPort}`, FLOW_RECORD_HYDRATION: 'on', FLOW_DOCUMENT_RECORDS: 'on', FLOW_TRASH_RECORDS: 'on', FLOW_INDEXED_READS: 'on', FLOW_SYNC_MANIFEST: 'on', FLOW_RECORD_JOURNAL: 'off', FLOW_CONNECTOR_SECRET_KEY: Buffer.alloc(32, 'p').toString('base64') }, stdio: ['ignore', 'pipe', 'pipe'] })
    api.stdout!.pipe(log); api.stderr!.pipe(log)
    const flags = scaleP20Flags ? '"true"' : '"false"'
    const vite = await createServer({ root: web, cacheDir: path.join(scratch, 'vite-cache'), configFile: path.join(web, 'vite.config.ts'), logLevel: 'error', define: {
      'import.meta.env.VITE_SYNC_V2': flags,
      'import.meta.env.VITE_DOCUMENT_WINDOWS': flags,
      'import.meta.env.VITE_TRASH_WINDOWS': flags,
      'import.meta.env.VITE_PEOPLE_WINDOWS': flags,
      'import.meta.env.VITE_PICKER_WINDOWS': flags,
    }, server: { host: '127.0.0.1', port: 0, hmr: false, watch: null, proxy: { '/api': { target: `http://127.0.0.1:${apiPort}`, ws: true } } } })
    const closeVite = trackSockets(vite)
    try {
      const deadline = Date.now() + 30_000
      let ready = false
      while (Date.now() < deadline && api.exitCode === null) {
        try { if ((await fetch(`http://127.0.0.1:${apiPort}/api/health`)).ok) { ready = true; break } } catch { /* Starting. */ }
        await new Promise(resolve => setTimeout(resolve, 100))
      }
      if (!ready) throw new Error(`P20 API did not start; see ${scratch}/api.log`)
      await vite.listen()
      await provide(`http://127.0.0.1:${(vite.httpServer!.address() as net.AddressInfo).port}`)
    } finally {
      await stop(api)
      await closeVite()
      log.end()
    }
  }, {}],
  scaleP20LegacyURL: async ({ scaleP20URL }, provide) => {
    const web = path.resolve(import.meta.dirname, '..')
    const define = Object.fromEntries(['VITE_SYNC_V2', 'VITE_DOCUMENT_WINDOWS', 'VITE_TRASH_WINDOWS', 'VITE_PEOPLE_WINDOWS', 'VITE_PICKER_WINDOWS'].map(flag => [`import.meta.env.${flag}`, '"false"']))
    const cacheDir = mkdtempSync(path.join(artifacts, 'legacy-cache-'))
    const vite = await createServer({ root: web, cacheDir, configFile: path.join(web, 'vite.config.ts'), logLevel: 'error', define, server: { host: '127.0.0.1', port: 0, hmr: false, watch: null, proxy: { '/api': { target: scaleP20URL, ws: true } } } })
    const closeVite = trackSockets(vite)
    try {
      await vite.listen()
      await provide(`http://127.0.0.1:${(vite.httpServer!.address() as net.AddressInfo).port}`)
    } finally { await closeVite() }
  },
  baseURL: async ({ scaleP20URL }, provide) => { await provide(scaleP20URL) },
})

import { spawn, spawnSync } from 'node:child_process'
import { mkdtempSync, rmSync } from 'node:fs'
import http from 'node:http'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import type { AddressInfo } from 'node:net'

export const PACKAGE_DIR = join(import.meta.dirname, '..')
export const TSX_BIN = join(PACKAGE_DIR, 'node_modules', '.bin', 'tsx')
export const MAIN_TS = join(PACKAGE_DIR, 'src', 'main.ts')
export const NODE_DIR = dirname(process.execPath)

/** Test servers listen on track C's port range (5121–5129) unless FLOW_TEST_PORT_BASE moves it. */
const PORT_BASE = Number(process.env.FLOW_TEST_PORT_BASE || 5122)

export interface TestServer {
  url: string
  requests: Array<{ method: string; url: string; headers: http.IncomingHttpHeaders; body: string }>
  close(): Promise<void>
}

export async function startServer(
  offset: number,
  handler: (req: http.IncomingMessage, body: string, res: http.ServerResponse) => void | Promise<void>,
): Promise<TestServer> {
  const requests: TestServer['requests'] = []
  const server = http.createServer((req, res) => {
    let body = ''
    req.on('data', (chunk) => (body += chunk))
    req.on('end', () => {
      requests.push({ method: req.method ?? '', url: req.url ?? '', headers: req.headers, body })
      void handler(req, body, res)
    })
  })
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject)
    server.listen(PORT_BASE + offset, '127.0.0.1', () => resolve())
  })
  const { port } = server.address() as AddressInfo
  return {
    url: `http://127.0.0.1:${port}`,
    requests,
    close: () =>
      new Promise((resolve) => {
        server.closeAllConnections()
        server.close(() => resolve())
      }),
  }
}

export function json(res: http.ServerResponse, status: number, body: unknown): void {
  res.writeHead(status, { 'content-type': 'application/json' })
  res.end(JSON.stringify(body))
}

export function tempDir(prefix: string): { path: string; cleanup: () => void } {
  const path = mkdtempSync(join(tmpdir(), `flow-runner-${prefix}-`))
  return { path, cleanup: () => rmSync(path, { recursive: true, force: true }) }
}

export function sh(command: string, args: string[], options: { cwd?: string; env?: NodeJS.ProcessEnv; input?: string } = {}) {
  const result = spawnSync(command, args, { encoding: 'utf8', ...options })
  if (result.status !== 0) throw new Error(`${command} ${args.join(' ')} failed (${result.status}): ${result.stderr}`)
  return result.stdout
}

/** Async variant: needed when the child talks to a server running in this process. */
export function shAsync(command: string, args: string[], options: { cwd?: string; env?: NodeJS.ProcessEnv; input?: string } = {}): Promise<string> {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { cwd: options.cwd, env: options.env, stdio: ['pipe', 'pipe', 'pipe'] })
    let stdout = ''
    let stderr = ''
    child.stdout.on('data', (chunk) => (stdout += chunk))
    child.stderr.on('data', (chunk) => (stderr += chunk))
    child.once('error', reject)
    child.once('close', (code) => (code === 0 ? resolve(stdout) : reject(new Error(`${command} ${args.join(' ')} failed (${code}): ${stderr}`))))
    child.stdin.end(options.input ?? '')
  })
}

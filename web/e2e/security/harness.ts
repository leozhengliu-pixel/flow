import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import fs from 'node:fs'
import http from 'node:http'
import https from 'node:https'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import { expect, type BrowserContext, type Page } from '@playwright/test'

/**
 * Browser checks of the security headers, run against the production build the Go server serves (the Vite dev server sends
 * no headers). Every spec starts its own API process with the policy under test, on a fresh database, so no test shares
 * state with another and at most one API runs at a time (workers: 1). The binary and the web build come from
 * global-setup.ts, or from FLOW_SECURITY_API_BIN / FLOW_SECURITY_WEB_DIST.
 */

export type ApiOptions = {
  /** FLOW_SECURITY_CSP; the specs default to enforce, which is what the strictest deployments run. */
  csp?: 'enforce' | 'report-only' | 'off'
  reportUri?: string
  /** The public origin (FLOW_APP_URL). Defaults to http://localhost:<port>: passkeys need a hostname, not an IP address. */
  appUrl?: string
  trustProxy?: boolean
  port?: number
  env?: Record<string, string>
}

export type RunningApi = {
  /** http://localhost:<port>, the origin a browser uses to reach the API directly. */
  origin: string
  port: number
  log: () => string
  stop: () => Promise<void>
}

export async function freePort(): Promise<number> {
  return await new Promise((resolve, reject) => {
    const server = net.createServer()
    server.once('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address() as net.AddressInfo
      server.close(() => resolve(port))
    })
  })
}

function required(name: string) {
  const value = process.env[name]
  if (!value) throw new Error(`${name} is not set; run through playwright.security.config.ts (global-setup builds it)`)
  return value
}

export async function startApi(options: ApiOptions = {}): Promise<RunningApi> {
  const port = options.port ?? (await freePort())
  const origin = `http://localhost:${port}`
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'flow-security-'))
  let output = ''
  const child: ChildProcess = spawn(required('FLOW_SECURITY_API_BIN'), [], {
    env: {
      PATH: process.env.PATH ?? '',
      HOME: directory,
      TMPDIR: directory,
      FLOW_DATABASE_PATH: path.join(directory, 'flow.db'),
      FLOW_HTTP_ADDR: `127.0.0.1:${port}`,
      FLOW_APP_URL: options.appUrl ?? origin,
      FLOW_STATIC_PATH: required('FLOW_SECURITY_WEB_DIST'),
      FLOW_DEV_AUTH_TOKENS: 'true',
      FLOW_WORKSPACE_TUTORIAL: 'off',
      FLOW_SECURITY_CSP: options.csp ?? 'enforce',
      ...(options.reportUri ? { FLOW_SECURITY_CSP_REPORT_URI: options.reportUri } : {}),
      ...(options.trustProxy ? { FLOW_TRUST_PROXY_HEADERS: 'true' } : {}),
      ...options.env,
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  child.stdout?.on('data', chunk => { output += chunk })
  child.stderr?.on('data', chunk => { output += chunk })
  const exited = new Promise<void>(resolve => child.once('exit', () => resolve()))
  const stop = async () => {
    if (child.exitCode === null) child.kill('SIGTERM')
    await Promise.race([exited, new Promise(resolve => setTimeout(resolve, 5000))])
    if (child.exitCode === null) child.kill('SIGKILL')
    fs.rmSync(directory, { recursive: true, force: true })
  }
  const deadline = Date.now() + 60_000
  for (;;) {
    if (child.exitCode !== null) throw new Error(`API exited with ${child.exitCode}\n${output}`)
    try {
      if ((await fetch(`http://127.0.0.1:${port}/api/health`)).ok) break
    } catch { /* not listening yet */ }
    if (Date.now() > deadline) {
      await stop()
      throw new Error(`API did not start\n${output}`)
    }
    await new Promise(resolve => setTimeout(resolve, 100))
  }
  return { origin, port, log: () => output, stop }
}

export type Account = { email: string; password: string; workspaceKey: string }

/**
 * A same-origin API call made by the browser itself, from a page already on the app's origin. The specs never call a
 * gateway's public host name from Node: *.localhost resolves in Chromium everywhere, but not in every system resolver.
 */
export async function apiFetch(page: Page, method: string, path: string, body?: unknown, workspaceKey?: string) {
  return await page.evaluate(async ({ method, path, body, workspaceKey }) => {
    const response = await fetch(path, {
      method,
      headers: { ...(body === undefined ? {} : { 'Content-Type': 'application/json' }), ...(workspaceKey ? { 'X-Workspace-Key': workspaceKey } : {}) },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const text = await response.text()
    let json: unknown
    try { json = JSON.parse(text) } catch { json = undefined }
    return { status: response.status, json: json as Record<string, unknown> | undefined, text }
  }, { method, path, body, workspaceKey })
}

/** Signs the account in, in this page's browser context (the session cookie), the way the login form does. */
export async function signIn(page: Page, account: Account) {
  const login = await apiFetch(page, 'POST', '/api/auth/login', { email: account.email, password: account.password })
  expect(login.status).toBe(200)
}

/**
 * Creates a verified account and a workspace through the API and leaves its session cookie in the browser context. The
 * account is made inside the test on the test's own database: nothing here is a real credential.
 */
export async function signUp(context: BrowserContext, origin: string, label: string): Promise<Account> {
  const account: Account = { email: `${label}@example.test`, password: 'correct-horse-battery-1', workspaceKey: `sec-${label}` }
  const page = await context.newPage()
  try {
    await page.goto(`${origin}/sw.js`)
    const registered = await apiFetch(page, 'POST', '/api/auth/register', { name: 'Security Tester', email: account.email, password: account.password })
    expect(registered.status).toBe(201)
    expect((await apiFetch(page, 'POST', '/api/auth/verify-email', { token: registered.json?.verificationToken })).status).toBe(200)
    await signIn(page, account)
    expect((await apiFetch(page, 'POST', '/api/workspaces', { name: 'Security workspace', urlKey: account.workspaceKey, tutorial: false })).status).toBe(201)
  } finally {
    await page.close()
  }
  return account
}

/** Creates a document through the API and opens it in the editor. */
export async function openNewDocument(page: Page, origin: string, account: Account, title = 'Security document') {
  await page.goto(`${origin}/sw.js`)
  const created = await apiFetch(page, 'POST', '/api/documents', { title, content: '' }, account.workspaceKey)
  expect(created.status).toBe(201)
  const document = created.json as { id: string; slugId: string }
  await page.goto(`${origin}/${account.workspaceKey}/document/${document.slugId}`)
  const editor = page.getByRole('textbox', { name: 'Document content' })
  await expect(editor).toBeEditable()
  return { editor, document }
}

/** Pastes plain text into the editor the way a browser does, so the editor's paste handlers run. */
export async function pasteText(page: Page, text: string) {
  await page.evaluate(text => {
    const target = document.querySelector<HTMLElement>('[aria-label="Document content"]')!
    target.focus()
    const data = new DataTransfer()
    data.setData('text/plain', text)
    target.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }))
  }, text)
}

/** Everything the browser reports as a Content Security Policy problem on a page, enforced or report-only. */
export type Violations = { all: () => string[]; clear: () => void }

export async function watchViolations(page: Page): Promise<Violations> {
  const seen: string[] = []
  // The browser words each one as "... violates the following Content Security Policy directive ..." (and "Refused to ..." in
  // older versions); a policy-feature problem reads "Permissions policy violation" or "... is not allowed in this document".
  const record = (text: string) => { if (/content security policy|refused to|violates the following|permissions policy|feature policy|not allowed in this document/i.test(text)) seen.push(text) }
  page.on('console', message => record(message.text()))
  page.on('pageerror', error => record(error.message))
  return { all: () => [...seen], clear: () => { seen.length = 0 } }
}

/** A web page that is not the app: serves a tiny HTML page (and optional JS) on a loopback port for iframe and report tests. */
export async function startStubServer(handler: http.RequestListener): Promise<{ origin: string; port: number; stop: () => Promise<void> }> {
  const server = http.createServer(handler)
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  const { port } = server.address() as net.AddressInfo
  return {
    origin: `http://127.0.0.1:${port}`,
    port,
    stop: () => new Promise<void>(resolve => { server.closeAllConnections(); server.close(() => resolve()) }),
  }
}

/**
 * A throwaway self-signed certificate for localhost, made with the openssl command line at run time (nothing is stored in
 * the repository). The security config starts the browser with --ignore-certificate-errors to accept it. Browsers deliver
 * Reporting API reports only to HTTPS endpoints from HTTPS pages, which is also how Flow runs in production, so the
 * report tests put an HTTPS gateway in front of Flow.
 */
function selfSignedCertificate() {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'flow-security-tls-'))
  const key = path.join(directory, 'key.pem')
  const cert = path.join(directory, 'cert.pem')
  execFileSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', key, '-out', cert, '-days', '1', '-subj', '/CN=localhost', '-addext', 'subjectAltName=DNS:localhost,DNS:*.localhost,IP:127.0.0.1'], { stdio: 'ignore' })
  return { key: fs.readFileSync(key), cert: fs.readFileSync(cert), remove: () => fs.rmSync(directory, { recursive: true, force: true }) }
}

/**
 * A reverse proxy standing in for the operator's gateway: it listens on a public host name, forwards to the API with the
 * Host header rewritten (as most gateways do) plus X-Forwarded-Host/-Proto, and tunnels WebSocket upgrades. A request
 * path in `intercept` is answered by the handler instead of the API (a CSP report collector on Flow's own origin).
 */
export async function startProxy(target: number, options: { intercept?: Record<string, http.RequestListener>; port?: number; tls?: boolean } = {}) {
  const { intercept, port: listenPort = 0, tls = false } = options
  const certificate = tls ? selfSignedCertificate() : undefined
  const forwardedProto = tls ? 'https' : 'http'
  const handle: http.RequestListener = (request, response) => {
    const handler = intercept?.[(request.url ?? '').split('?')[0]]
    if (handler) return handler(request, response)
    const upstream = http.request({
      host: '127.0.0.1', port: target, method: request.method, path: request.url,
      headers: { ...request.headers, host: `127.0.0.1:${target}`, 'x-forwarded-host': request.headers.host ?? '', 'x-forwarded-proto': forwardedProto },
    }, answer => { response.writeHead(answer.statusCode ?? 502, answer.headers); answer.pipe(response) })
    upstream.on('error', () => { response.statusCode = 502; response.end() })
    request.pipe(upstream)
  }
  const server = certificate ? https.createServer({ key: certificate.key, cert: certificate.cert }, handle) : http.createServer(handle)
  const sockets = new Set<{ destroy(): unknown }>()
  server.on('connection', socket => { sockets.add(socket); socket.once('close', () => sockets.delete(socket)) })
  server.on('upgrade', (request, client, head) => {
    const upstream = net.connect(target, '127.0.0.1', () => {
      const headers = { ...request.headers, host: `127.0.0.1:${target}`, 'x-forwarded-host': request.headers.host ?? '', 'x-forwarded-proto': forwardedProto }
      upstream.write(`${request.method} ${request.url} HTTP/1.1\r\n${Object.entries(headers).map(([name, value]) => `${name}: ${value}`).join('\r\n')}\r\n\r\n`)
      upstream.write(head)
      client.pipe(upstream).pipe(client)
    })
    upstream.on('error', () => client.destroy())
    client.on('error', () => upstream.destroy())
    sockets.add(client).add(upstream)
  })
  await new Promise<void>(resolve => server.listen(listenPort, '127.0.0.1', resolve))
  const { port } = server.address() as net.AddressInfo
  return {
    port,
    stop: () => new Promise<void>(resolve => { sockets.forEach(socket => socket.destroy()); server.close(() => { certificate?.remove(); resolve() }) }),
  }
}

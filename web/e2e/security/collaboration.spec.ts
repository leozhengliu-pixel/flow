import { expect, test, type Browser, type Page } from '@playwright/test'
import { freePort, openNewDocument, signIn, signUp, startApi, startProxy, startStubServer, watchViolations, type Account } from './harness'

type Origin = { origin: string; account: Account; documentUrl: string }

/** A second browser context signed in as the same account (a second device) and opened on the same document. */
async function secondDevice(browser: Browser, site: Origin) {
  const context = await browser.newContext()
  const page = await context.newPage()
  await page.goto(`${site.origin}/sw.js`)
  await signIn(page, site.account)
  return { context, page }
}

function socketsOf(page: Page) {
  const sockets: Array<{ url: string; received: number; closed: boolean; error: boolean }> = []
  page.on('websocket', socket => {
    const entry = { url: socket.url(), received: 0, closed: false, error: false }
    sockets.push(entry)
    socket.on('framereceived', () => { entry.received++ })
    socket.on('close', () => { entry.closed = true })
    socket.on('socketerror', () => { entry.error = true })
  })
  return sockets
}

async function collaborate(browser: Browser, page: Page, site: Origin, label: string) {
  const violations = await watchViolations(page)
  const sockets = socketsOf(page)
  await page.goto(site.documentUrl)
  const editor = page.getByRole('textbox', { name: 'Document content' })
  await expect(editor).toBeEditable()

  const other = await secondDevice(browser, site)
  try {
    const otherViolations = await watchViolations(other.page)
    const otherSockets = socketsOf(other.page)
    await other.page.goto(site.documentUrl)
    const otherEditor = other.page.getByRole('textbox', { name: 'Document content' })
    await expect(otherEditor).toBeEditable()

    // Both editors hold a collaboration socket on the public origin's host, upgraded under the enforced connect-src.
    const host = new URL(site.origin).host
    await expect.poll(() => sockets.some(socket => socket.url.startsWith(`ws://${host}/api/realtime/socket`) && !socket.closed), { message: `${label}: first socket` }).toBe(true)
    await expect.poll(() => otherSockets.some(socket => socket.url.startsWith(`ws://${host}/api/realtime/socket`) && !socket.closed), { message: `${label}: second socket` }).toBe(true)

    // An edit made on one device appears on the other, and the reverse, through the socket.
    await editor.click()
    await page.keyboard.type(`from the first device ${label}`)
    await expect(otherEditor).toContainText(`from the first device ${label}`, { timeout: 15_000 })
    await otherEditor.click()
    await other.page.keyboard.press('Control+End')
    await other.page.keyboard.type(` and the second ${label}`)
    await expect(editor).toContainText(`and the second ${label}`, { timeout: 15_000 })
    await expect.poll(() => sockets.reduce((count, socket) => count + socket.received, 0)).toBeGreaterThan(0)
    expect([...sockets, ...otherSockets].filter(socket => socket.error)).toEqual([])
    expect(violations.all()).toEqual([])
    expect(otherViolations.all()).toEqual([])
  } finally {
    await other.context.close()
  }
}

test('two devices edit one document through the collaboration socket under the enforced policy', async ({ browser, context, page }) => {
  const api = await startApi({ csp: 'enforce' })
  try {
    const account = await signUp(context, api.origin, 'collab')
    const { document } = await openNewDocument(page, api.origin, account)
    await collaborate(browser, page, { origin: api.origin, account, documentUrl: `${api.origin}/${account.workspaceKey}/document/${document.slugId}` }, 'direct')
  } finally {
    await api.stop()
  }
})

for (const mismatch of [false, true]) {
  test(`collaboration works behind a gateway on its own public host${mismatch ? ' even when FLOW_APP_URL names another host' : ''}`, async ({ browser, context, page }) => {
    // The gateway rewrites Host and sends X-Forwarded-Host/Proto; the browser only ever sees the gateway's name.
    // *.localhost resolves to loopback in Chromium, so the public host needs no DNS entry.
    const gatewayPort = await freePort()
    const publicOrigin = `http://flow-public.localhost:${gatewayPort}`
    const api = await startApi({ csp: 'enforce', trustProxy: true, appUrl: mismatch ? 'https://flow.example.test' : publicOrigin })
    const gateway = await startProxy(api.port, { port: gatewayPort })
    try {
      const account = await signUp(context, publicOrigin, mismatch ? 'collab-mismatch' : 'collab-gateway')
      const { document } = await openNewDocument(page, publicOrigin, account)
      const policy = (await page.goto(`${publicOrigin}/${account.workspaceKey}`))!.headers()['content-security-policy']
      expect(policy).toContain("connect-src 'self'")
      if (!mismatch) expect(policy).toContain(`ws://flow-public.localhost:${gatewayPort}`)
      await collaborate(browser, page, { origin: publicOrigin, account, documentUrl: `${publicOrigin}/${account.workspaceKey}/document/${document.slugId}` }, mismatch ? 'mismatch' : 'gateway')
    } finally {
      await gateway.stop()
      await api.stop()
    }
  })
}

test('the collaboration socket refuses a page on another origin', async ({ page, context }) => {
  const api = await startApi({ csp: 'enforce' })
  const stub = await startStubServer((_request, response) => { response.setHeader('content-type', 'text/html'); response.end('<!doctype html><title>other site</title>') })
  try {
    const account = await signUp(context, api.origin, 'collab-origin')
    await page.goto(stub.origin)
    const outcome = await page.evaluate(url => new Promise<string>(resolve => {
      const socket = new WebSocket(url)
      socket.onopen = () => resolve('open')
      socket.onerror = () => resolve('refused')
      setTimeout(() => resolve('timeout'), 5000)
    }), `ws://localhost:${api.port}/api/realtime/socket?workspace=${account.workspaceKey}&clientId=other-site`)
    expect(outcome).toBe('refused')
  } finally {
    await stub.stop()
    await api.stop()
  }
})

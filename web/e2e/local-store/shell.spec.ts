import { expect } from '@playwright/test'
import { createServer, request as httpRequest } from 'node:http'
import type { AddressInfo } from 'node:net'
import { reloadDocument, test as workspace } from './fixture'

const test = workspace.extend<{ shellOrigin: { url: string; disconnect: () => Promise<void> } }>({
  shellOrigin: async ({ baseURL }, provide) => {
    const upstream = new URL(baseURL!)
    const proxy = createServer((incoming, outgoing) => {
      const request = httpRequest({
        hostname: upstream.hostname, port: upstream.port, path: incoming.url,
        method: incoming.method, headers: incoming.headers,
      }, response => {
        outgoing.writeHead(response.statusCode ?? 502, response.headers)
        response.pipe(outgoing)
      })
      outgoing.on('close', () => request.destroy())
      request.on('error', () => outgoing.destroy())
      incoming.pipe(request)
    })
    await new Promise<void>((resolve, reject) => {
      proxy.once('error', reject)
      proxy.listen(0, '127.0.0.1', resolve)
    })
    const disconnect = () => new Promise<void>(resolve => {
      proxy.close(() => resolve())
      proxy.closeAllConnections()
    })
    const url = `http://127.0.0.1:${(proxy.address() as AddressInfo).port}`
    try { await provide({ url, disconnect }) }
    finally { if (proxy.listening) await disconnect() }
  },
})

test.use({ workspaceNamePrefix: 'Shell' })

test('activates the production app-shell worker alongside the local store', async ({ page, context, request, workspaceKey, shellOrigin }) => {
  if (process.env.FLOW_LOCAL_STORE_REAL_AUTH === '1') {
    await context.addCookies((await request.storageState()).cookies)
  }
  const headers = { 'X-Workspace-Key': workspaceKey }
  const settings = await request.patch('/api/account/settings', { headers, data: { pulseSchedule: 'never', timezone: 'UTC' } })
  expect(settings.ok()).toBe(true)
  const bootstrap = await request.get('/api/bootstrap', { headers })
  expect(bootstrap.ok()).toBe(true)
  const { teams } = await bootstrap.json()
  const title = 'Production shell fixture'
  expect((await request.post('/api/issues', { headers, data: { teamId: teams[0].id, title } })).status()).toBe(201)
  const loaded = page.waitForResponse(response => ['/api/bootstrap', '/api/issue-records/bootstrap'].includes(new URL(response.url()).pathname))
  const caughtUp = page.waitForResponse(response => new URL(response.url()).searchParams.get('surface') === 'local-store' && new URL(response.url()).pathname === '/api/sync/changes')
  await page.goto(`${shellOrigin.url}/${workspaceKey}/team/${teams[0].key.toLowerCase()}/all`)
  await expect(page.getByText(title, { exact: true })).toBeVisible()
  expect((await loaded).headers()['x-flow-local-store']).toBeDefined()
  expect((await caughtUp).status()).toBe(200)
  await expect.poll(() => page.evaluate(() => navigator.serviceWorker.controller?.state)).toBe('activated')
  const checksumCount = () => page.evaluate(() => performance.getEntriesByName('flow:local-store:checksum-match').length)
  await expect.poll(checksumCount, { timeout: 15_000 }).toBeGreaterThan(0)
  await reloadDocument(page)
  await expect(page.getByText(title, { exact: true })).toBeVisible()
  await expect.poll(checksumCount, { timeout: 15_000 }).toBeGreaterThan(0)
  await expect.poll(() => page.evaluate(async () => !!await (await caches.open('flow-shell-v2')).match('/__flow-app-shell'))).toBe(true)
  await page.evaluate(async () => { await (await navigator.serviceWorker.getRegistration())!.update() })
  await expect.poll(() => page.evaluate(() => navigator.serviceWorker.controller?.state)).toBe('activated')

  const apiPath = '/api/sync/manifest?surface=local-store'
  expect((await page.evaluate(async ({ apiPath, workspaceKey }) => {
    return (await fetch(apiPath, { headers: { 'X-Workspace-Key': workspaceKey } })).status
  }, { apiPath, workspaceKey }))).toBe(200)
  // WebKit's setOffline can fail requests before the worker receives them.
  // Disconnect this test's origin so the browser still exercises the worker.
  await shellOrigin.disconnect()
  const result = await page.evaluate(async ({ apiPath, workspaceKey }) => {
    const asset = (await (await caches.open('flow-shell-v2')).keys()).find(entry => new URL(entry.url).pathname.startsWith('/assets/'))!
    const cached = await fetch(asset.url, { cache: 'no-store' })
    const api = await fetch(apiPath, { headers: { 'X-Workspace-Key': workspaceKey } }).catch(() => null)
    return { asset: cached.status, api: api?.status ?? null }
  }, { apiPath, workspaceKey })
  expect(result).toEqual({ asset: 200, api: null })
})

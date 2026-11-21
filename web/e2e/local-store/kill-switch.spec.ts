import { expect } from '@playwright/test'
import { test } from './fixture'

test.use({ workspaceNamePrefix: 'Kill-switch' })
test.skip(process.env.FLOW_LOCAL_STORE_CLIENT !== 'off', 'Requires the flag-off client')

test('flag off deletes a retained database before using the legacy loader', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const { viewer, workspace, teams } = await (await request.get('/api/bootstrap', { headers })).json()
  const title = 'Loaded from the workspace API'
  const team = teams[0]
  expect((await request.post('/api/issues', { headers, data: { teamId: team.id, title } })).status()).toBe(201)

  const fixtureURL = '**/__local_store_fixture__'
  await page.route(fixtureURL, route => route.fulfill({ contentType: 'text/html', body: '<!doctype html><title>Storage fixture</title>' }))
  await page.goto('/__local_store_fixture__')
  const name = `flow-ls:${encodeURIComponent(viewer.id)}:${encodeURIComponent(workspace.id)}`
  await page.evaluate(({ name, workspaceKey }) => new Promise<void>((resolve, reject) => {
    localStorage.setItem(`flow:local-store:${name}`, JSON.stringify({ workspaceKey, authzEpoch: '0', token: 'fixture' }))
    const open = indexedDB.open(name, 1)
    open.onupgradeneeded = () => open.result.createObjectStore('issue').put('Retained data', 'test')
    open.onerror = () => reject(open.error)
    open.onsuccess = () => { open.result.close(); resolve() }
  }), { name, workspaceKey })
  await page.unroute(fixtureURL)
  // Exercise the index-only fallback on engines without database enumeration.
  await page.addInitScript(() => Object.defineProperty(indexedDB, 'databases', { configurable: true, value: undefined }))

  const bootstrap = page.waitForResponse(response => new URL(response.url()).pathname === '/api/bootstrap')
  await page.goto(`/${workspaceKey}/team/${team.key.toLowerCase()}/all`, { waitUntil: 'domcontentloaded' })
  expect((await bootstrap).ok()).toBe(true)
  await expect(page.getByText(title, { exact: true })).toBeVisible()
  await expect.poll(() => page.evaluate(name => localStorage.getItem(`flow:local-store:${name}`), name)).toBeNull()
  expect(await page.evaluate(name => new Promise<boolean>((resolve, reject) => {
    const open = indexedDB.open(name)
    open.onupgradeneeded = () => open.transaction?.abort()
    open.onerror = () => open.error?.name === 'AbortError' ? resolve(false) : reject(open.error)
    open.onsuccess = () => { open.result.close(); resolve(true) }
  }), name)).toBe(false)
})

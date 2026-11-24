import { expect } from '@playwright/test'
import { readLocalRow, test } from './fixture'

// Switch injection uses Playwright routing. The worker is covered by shell.spec.
test.use({ workspaceNamePrefix: 'Kill-switch', serviceWorkers: 'block' })

test('operator opt-out deletes retained databases and uses network reads', async ({ page, request, workspaceKey }) => {
  test.skip(process.env.FLOW_LOCAL_STORE !== 'off', 'Requires the server opt-out')
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

  const session = page.waitForResponse(response => new URL(response.url()).pathname === '/api/auth/session')
  const bootstrap = page.waitForResponse(response => new URL(response.url()).pathname === '/api/bootstrap')
  await page.goto(`/${workspaceKey}/team/${team.key.toLowerCase()}/all`, { waitUntil: 'domcontentloaded' })
  expect((await session).headers()['x-flow-local-store']).toBe('disabled')
  expect((await bootstrap).ok()).toBe(true)
  await expect(page.getByText(title, { exact: true })).toBeVisible()
  expect(await page.evaluate(() => performance.getEntriesByName('flow:local-store:erase:kill-switch', 'mark').length)).toBe(1)
  await expect.poll(() => page.evaluate(name => localStorage.getItem(`flow:local-store:${name}`), name)).toBeNull()
  expect(await page.evaluate(name => new Promise<boolean>((resolve, reject) => {
    const open = indexedDB.open(name)
    open.onupgradeneeded = () => open.transaction?.abort()
    open.onerror = () => open.error?.name === 'AbortError' ? resolve(false) : reject(open.error)
    open.onsuccess = () => { open.result.close(); resolve(true) }
  }), name)).toBe(false)
})

test('a running tab broadcasts the server opt-out to other tabs', async ({ page, context, request, workspaceKey }) => {
  test.skip(process.env.FLOW_LOCAL_STORE === 'off', 'Requires a retained issue store')
  const headers = { 'X-Workspace-Key': workspaceKey }
  expect((await request.patch('/api/account/settings', { headers, data: { pulseSchedule: 'never', timezone: 'UTC' } })).ok()).toBe(true)
  const { teams: [team] } = await (await request.get('/api/bootstrap', { headers })).json()
  const title = 'Issue rendered after the opt-out'
  const create = await request.post('/api/issues', { headers, data: { teamId: team.id, title } })
  expect(create.status()).toBe(201)
  const { id } = await create.json()
  const path = `/${workspaceKey}/team/${team.key.toLowerCase()}/all`
  const other = await context.newPage()
  for (const tab of [page, other]) {
    await tab.goto(path)
    await expect(tab.getByText(title, { exact: true })).toBeVisible()
    await expect.poll(() => readLocalRow(tab, 'workspace', '_meta')).toHaveProperty('resume')
  }
  await page.bringToFront()
  let optOutReplies = 0
  await page.route(url => ['/api/sync/changes', '/api/sync/manifest'].includes(url.pathname) && url.searchParams.get('surface') === 'local-store',
    async route => {
      optOutReplies++
      await route.fulfill({ status: 404, headers: { 'X-Flow-Local-Store': 'disabled' } })
    })
  const network = [page, other].map(tab => {
    const responses: number[] = []
    tab.on('response', response => {
      if (new URL(response.url()).pathname !== '/api/bootstrap') return
      if (!response.request().headers()['x-flow-local-store']) responses.push(response.status())
    })
    return responses
  })
  await page.evaluate(() => window.dispatchEvent(new Event('online')))
  await expect.poll(() => optOutReplies, { message: 'waking tab received the operator opt-out' }).toBeGreaterThan(0)
  await expect.poll(() => network.map(responses => responses.includes(200)), {
    message: 'both tabs loaded the workspace over the network after opt-out',
  }).toEqual([true, true])
  for (const tab of [page, other]) {
    await expect(tab.getByText(title, { exact: true })).toBeVisible()
    await expect.poll(() => readLocalRow(tab, id)).toBeUndefined()
    expect(await tab.evaluate(() => performance.getEntriesByName('flow:local-store:erase:kill-switch', 'mark').length)).toBe(1)
  }
})

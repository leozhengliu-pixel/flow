import { expect, test, type Page } from '@playwright/test'
import { createFixtureWorkspace } from '../workspace-fixture'
import { apiFetch, signUp, startApi, type RunningApi } from './harness'

test.use({ serviceWorkers: 'block', viewport: { width: 1440, height: 900 }, timezoneId: 'UTC' })

async function switchWorkspace(page: Page, name: string) {
  await page.locator('.workspace-menu-trigger').click()
  await page.locator('[data-workspace-switch-trigger]').hover()
  await page.locator('.workspace-switch-menu').getByRole('menuitem', { name, exact: true }).click()
}

test.describe('upgrade from the startup snapshot', () => {
  let api: RunningApi
  test.beforeAll(async () => {
    api = await startApi({ env: {
      FLOW_SYNC_V2: 'on', FLOW_SYNC_MANIFEST: 'on', FLOW_INDEXED_READS: 'on',
      FLOW_RECORD_JOURNAL: 'on', FLOW_RECORD_HYDRATION: 'on', FLOW_LOCAL_STORE: 'on',
    } })
  })
  test.afterAll(async () => { await api?.stop() })

  test('removes the snapshot database and waits for live authorization before showing issues', async ({ page, context }) => {
    const { workspaceKey } = await signUp(context, api.origin, 'store-upgrade')
    await page.goto(`${api.origin}/sw.js`)
    const settings = await apiFetch(page, 'PATCH', '/api/account/settings', { timezone: 'UTC', pulseSchedule: 'never' }, workspaceKey)
    expect(settings.status).toBe(200)
    const bootstrap = await apiFetch(page, 'GET', '/api/bootstrap', undefined, workspaceKey)
    expect(bootstrap.status).toBe(200)
    const [team] = bootstrap.json!.teams as { id: string; key: string }[]
    const created = await apiFetch(page, 'POST', '/api/issues', { teamId: team.id, title: 'Confirmed issue' }, workspaceKey)
    expect(created.status).toBe(201)
    await page.evaluate(async key => {
      await new Promise<void>((resolve, reject) => {
        const request = indexedDB.open('flow-local-snapshot', 1)
        request.onerror = () => reject(request.error)
        request.onupgradeneeded = () => request.result.createObjectStore('snapshots', { keyPath: 'key' })
        request.onsuccess = () => {
          const db = request.result
          const tx = db.transaction('snapshots', 'readwrite')
          tx.objectStore('snapshots').put({ key, token: 'retired', json: JSON.stringify({ title: 'Retired issue' }) })
          tx.onerror = () => { db.close(); reject(tx.error) }
          tx.oncomplete = () => { db.close(); resolve() }
        }
      })
      localStorage.setItem('flow:local-snapshot', JSON.stringify({ [key]: 'retired' }))
    }, `${(bootstrap.json!.viewer as { id: string }).id}:${workspaceKey}`)

    let release!: () => void
    const held = new Promise<void>(resolve => { release = resolve })
    await page.route('**/api/auth/session', async route => {
      await held
      await route.continue()
    })
    const sessionRequested = page.waitForRequest('**/api/auth/session')
    try {
      await page.goto(`${api.origin}/${workspaceKey}/team/${team.key.toLowerCase()}/all`, { waitUntil: 'commit' })
      await sessionRequested
      await expect.poll(() => page.evaluate(async () => ({
        index: localStorage.getItem('flow:local-snapshot'),
        retired: (await indexedDB.databases()).some(db => db.name === 'flow-local-snapshot'),
      }))).toEqual({ index: null, retired: false })
      await expect(page.locator('.flow-startup')).toBeVisible()
      await expect(page.locator('[data-issue-row-id]')).toHaveCount(0)
    } finally {
      release()
    }
    await expect(page.getByText('Confirmed issue', { exact: true })).toBeVisible()
    await expect(page.getByText('Retired issue', { exact: true })).toHaveCount(0)
    await expect.poll(() => page.evaluate(async () =>
      (await indexedDB.databases()).some(db => db.name?.startsWith('flow-ls:')),
    )).toBe(true)
  })
})

test.describe('unconfirmed in-app workspace switch', () => {
  let api: RunningApi
  test.beforeAll(async () => { api = await startApi({ env: { FLOW_SYNC_V2: 'off' } }) })
  test.afterAll(async () => { await api?.stop() })

  test('waits for the destination bootstrap before sending a write', async ({ page, context, playwright }) => {
    const { workspaceKey } = await signUp(context, api.origin, 'navigation-write-gate')
    const request = await playwright.request.newContext({ baseURL: api.origin, storageState: await context.storageState() })
    const other = await createFixtureWorkspace(request, 'Other workspace', 'sec-navigation-other').finally(() => request.dispose())
    await page.addInitScript(() => localStorage.setItem('flow:locale', 'en-US'))
    await page.goto(`${api.origin}/${workspaceKey}/agent`)
    await expect(page.locator('.workspace-menu-trigger strong')).toHaveText('Security workspace')
    await expect.poll(() => page.evaluate(key => {
      const entry = Object.keys(localStorage).find(item => item.startsWith('flow:navigation:') && item.endsWith(`:${key}`))
      return entry ? JSON.parse(localStorage.getItem(entry)!).data.states.length : 0
    }, workspaceKey)).toBeGreaterThan(0)
    await switchWorkspace(page, `Other workspace ${other.urlKey.slice(-8)}`)
    await expect(page.locator('.workspace-menu-trigger strong')).toContainText('Other workspace')

    let release!: () => void
    const held = new Promise<void>(resolve => { release = resolve })
    const order: string[] = []
    const isBootstrap = (url: URL) => url.pathname === '/api/bootstrap'
    await page.route(isBootstrap, async route => {
      if (route.request().headers()['x-workspace-key'] === workspaceKey) await held
      await route.continue()
    })
    page.on('request', request => {
      if (new URL(request.url()).pathname === '/api/issues' && request.method() === 'POST') {
        order.push(request.headers()['x-workspace-key'])
      }
    })
    try {
      const loading = page.waitForRequest(request =>
        isBootstrap(new URL(request.url())) && request.headers()['x-workspace-key'] === workspaceKey,
      )
      await switchWorkspace(page, 'Security workspace')
      await loading
      await expect(page.locator('.workspace-menu-trigger strong')).toHaveText('Security workspace')
      await page.locator('.sidebar').click({ position: { x: 4, y: 50 } })
      await page.keyboard.press('c')
      const composer = page.locator('[data-flow-motion="create"]')
      const title = 'Created after workspace confirmation'
      await composer.locator('.title-editor').fill(title)
      await composer.getByRole('button', { name: 'Create issue', exact: true }).click()
      expect(await composer.getByRole('alert').allTextContents()).toEqual([])
      await expect(composer.locator('button[type="submit"]')).toBeDisabled()
      expect(order).toEqual([])
      const saved = page.waitForResponse(response =>
        new URL(response.url()).pathname === '/api/issues' && response.request().method() === 'POST',
      )
      order.push('released')
      release()
      const response = await saved
      expect(response.status()).toBe(201)
      expect(await response.json()).toMatchObject({ title })
      expect(order).toEqual(['released', workspaceKey])
    } finally {
      release()
    }
  })
})

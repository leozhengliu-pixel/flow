import { expect, type Page } from '@playwright/test'
import { join } from 'node:path'
import { editIssueTitle, readLocalRow, reloadDocument, test } from './fixture'

test.use({ serviceWorkers: 'block' })

test.beforeEach(async ({ page }) => {
  // Exercise the shadow-only protocol used by servers without issue resume.
  await page.route(url => url.pathname === '/api/sync/manifest' && url.searchParams.get('mode') === 'issues',
    route => route.fulfill({ status: 404 }))
})

async function measureEntries(page: Page, kind: string) {
  return page.evaluate(kind => performance.getEntriesByName(`flow:local-store:${kind}`).map(entry => entry.duration), kind)
}

async function nextDay(page: Page) {
  await page.evaluate(() => {
    const original = Date.now.bind(Date)
    Date.now = () => original() + 24 * 60 * 60_000 + 1000
    window.dispatchEvent(new Event('online'))
  })
}

test('checks bootstrap parity, delta cost, divergence and cleanup in IndexedDB', async ({ page, context, browser, request, workspaceKey }, info) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const settings = await request.patch('/api/account/settings', {
    headers, data: { pulseSchedule: 'never', language: 'en-US', interfaceTheme: 'Light', timezone: 'UTC' },
  })
  expect(settings.status()).toBe(200)
  const bootstrap = await request.get('/api/bootstrap', { headers })
  expect(bootstrap.status()).toBe(200)
  const { teams } = await bootstrap.json()
  const team = teams[0]
  const response = await request.post('/api/issues', { headers, data: { teamId: team.id, title: 'Journal seed', description: 'Editor body stays on its loader' } })
  expect(response.status()).toBe(201)
  const { id } = await response.json()
  const path = `/${workspaceKey}/team/${team.key.toLowerCase()}/all`
  const manifest = await request.get('/api/sync/manifest?surface=local-store', { headers })
  expect(manifest.status()).toBe(200)
  const baseline = await manifest.json()
  expect(baseline.capabilities).toEqual(['local-store-shadow-v1'])

  // Safari has no requestIdleCallback in some releases. Exercise the timer
  // path in all engines, with the optional storage estimate missing.
  await context.addInitScript(() => {
    Object.defineProperty(window, 'requestIdleCallback', { configurable: true, value: undefined })
    if (navigator.storage) Object.defineProperty(navigator.storage, 'estimate', { configurable: true, value: undefined })
    localStorage.setItem('flow:locale', 'en-US')
  })
  await page.emulateMedia({ colorScheme: 'light' })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto(path)
  await expect(page.getByText('Journal seed')).toBeVisible()
  await expect.poll(async () => (await measureEntries(page, 'checksum-match')).length).toBeGreaterThan(0)
  await expect.poll(() => readLocalRow(page, id)).toMatchObject({ id, title: 'Journal seed', _v: null })
  expect(await readLocalRow(page, id)).not.toHaveProperty('description')

  const batches = 40
  const initialChecks = (await measureEntries(page, 'checksum-match')).length
  for (let i = 1; i <= batches; i++) {
    const title = `Journal change ${i}`
    const changed = await request.patch(`/api/issues/${id}`, { headers, data: { title } })
    expect(changed.status()).toBe(200)
    await expect.poll(async () => (await measureEntries(page, 'checksum-match')).length, { intervals: [25, 50, 100] }).toBeGreaterThanOrEqual(initialChecks + i)
    expect(await readLocalRow(page, id)).toMatchObject({ title, _v: expect.stringMatching(/^\d+$/) })
  }
  await expect(page.getByText(`Journal change ${batches}`, { exact: true })).toBeVisible()
  const samples = (await measureEntries(page, 'delta-write')).sort((a, b) => a - b)
  const p95 = samples[Math.ceil(samples.length * 0.95) - 1]
  const mismatches = (await measureEntries(page, 'checksum-mismatch')).length
  const checks = (await measureEntries(page, 'checksum-match')).length
  console.log(`local-store ${info.project.name}: ${checks} checksums, ${mismatches} mismatches, ${samples.length} batches, p95=${p95?.toFixed(2)} ms`)
  expect(samples).toHaveLength(batches)
  expect(mismatches).toBe(0)
  expect.soft(p95).toBeLessThan(8)

  // At the next manifest reconciliation, a row changed outside its cursor
  // transaction must be detected and the database removed.
  await page.evaluate(async id => {
    const { name } = (await indexedDB.databases()).find(db => db.name?.startsWith('flow-ls:'))!
    await new Promise<void>((resolve, reject) => {
      const open = indexedDB.open(name!)
      open.onerror = () => reject(open.error)
      open.onsuccess = () => {
        const db = open.result
        const tx = db.transaction('issue', 'readwrite')
        tx.objectStore('issue').delete(id)
        tx.oncomplete = () => { db.close(); resolve() }
        tx.onabort = () => { db.close(); reject(tx.error) }
      }
    })
  }, id)
  const manifestCheck = page.waitForResponse(response => response.url().includes('/api/sync/manifest?surface=local-store'))
  await nextDay(page)
  expect((await manifestCheck).ok()).toBe(true)
  await expect.poll(() => measureEntries(page, 'count-match')).toHaveLength(1)
  await expect.poll(() => measureEntries(page, 'checksum-mismatch')).toHaveLength(1)
  await expect.poll(() => readLocalRow(page, id)).toBeUndefined()
  await expect(page.getByText(`Journal change ${batches}`, { exact: true })).toBeVisible()

  // Without the resume capability the existing loader still seeds the shadow
  // and confirms edits after a restart.
  await reloadDocument(page)
  await expect.poll(() => readLocalRow(page, id)).toMatchObject({ title: `Journal change ${batches}` })

  const editedTitle = 'Title edited in this tab'
  await page.getByText(`Journal change ${batches}`, { exact: true }).click()
  const editor = page.getByRole('textbox', { name: 'Issue title', exact: true })
  const saved = page.waitForResponse(response => response.request().method() === 'PATCH' &&
    [`/api/issues/${id}`, `/api/issue-records/${id}`].includes(new URL(response.url()).pathname), { timeout: 10_000 })
  await editIssueTitle(editor, editedTitle)
  expect((await saved).status()).toBe(200)
  await expect.poll(() => readLocalRow(page, id)).toMatchObject({ title: editedTitle })
  await page.goBack()
  await expect(page.getByText(editedTitle, { exact: true })).toBeVisible()

  const shots = process.env.FLOW_LOCAL_STORE_SHOTS
  if (shots) {
    const width = info.project.name === 'webkit' ? 375 : 390
    for (const [viewport, theme, language] of [
      [{ width: 1440, height: 900 }, 'light', 'en-US'],
      [{ width: 1440, height: 900 }, 'dark', 'zh-CN'],
      [{ width, height: 844 }, 'light', 'en-US'],
      [{ width, height: 844 }, 'dark', 'zh-CN'],
    ] as const) {
      const settings = await request.patch('/api/account/settings', { headers, data: { language, interfaceTheme: theme === 'light' ? 'Light' : 'Dark' } })
      expect(settings.ok()).toBe(true)
      const review = await browser.newContext({ viewport, colorScheme: theme, locale: language })
      try {
        const view = await review.newPage()
        await view.goto(new URL(path, page.url()).href)
        await expect(view.locator('html')).toHaveAttribute('lang', language === 'en-US' ? 'en' : language)
        await expect(view.locator('html')).toHaveAttribute('data-theme', theme)
        await expect(view.getByText(editedTitle, { exact: true })).toBeVisible()
        await expect.poll(() => readLocalRow(view, id)).toMatchObject({ title: editedTitle })
        expect(await view.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(viewport.width)
        await view.screenshot({ path: join(shots, `${info.project.name}-${viewport.width}-${theme}-${language}.png`), animations: 'disabled' })
      } finally {
        await review.close()
      }
    }
  }

  // Label writes currently invalidate the collection instead of supplying an
  // upsert. The shadow must recover from a newly authorized baseline.
  const label = await request.post('/api/labels', { headers, data: { name: 'Journal label', color: '#676f88' } })
  expect(label.status()).toBe(201)
  const { id: labelId } = await label.json()
  await page.evaluate(() => window.dispatchEvent(new Event('online')))
  await expect.poll(() => readLocalRow(page, labelId, 'label')).toMatchObject({ name: 'Journal label', _v: null })
})

test.describe('storage errors', () => {
  test.use({ workspaceNamePrefix: 'Storage' })

  test('keeps the network path when opening or writing IndexedDB fails', async ({ page, context, request, workspaceKey }) => {
    const headers = { 'X-Workspace-Key': workspaceKey }
    const { teams } = await (await request.get('/api/bootstrap', { headers })).json()
    const title = 'Network fallback issue'
    const issue = await request.post('/api/issues', { headers, data: { teamId: teams[0].id, title } })
    expect(issue.status()).toBe(201)
    const { id } = await issue.json()
    await context.addInitScript(() => {
      Object.defineProperty(IDBTransaction.prototype, 'commit', { configurable: true, value: undefined })
      const open = IDBFactory.prototype.open
      IDBFactory.prototype.open = function (name, ...args) {
        if (name.startsWith('flow-ls:') && !sessionStorage.getItem('fixture-allow-idb')) {
          performance.mark('fixture:storage-denied')
          throw new DOMException('Fixture denied IndexedDB', 'SecurityError')
        }
        return open.call(this, name, ...args)
      }
      const put = IDBObjectStore.prototype.put
      IDBObjectStore.prototype.put = function (...args) {
        if (this.name === 'issue' && this.transaction.db.name.startsWith('flow-ls:') && sessionStorage.getItem('fixture-deny-write')) {
          performance.mark('fixture:quota-exceeded')
          throw new DOMException('Fixture refused the IndexedDB write', 'QuotaExceededError')
        }
        return put.apply(this, args)
      }
    })
    await page.goto(`/${workspaceKey}/team/${teams[0].key.toLowerCase()}/all`)
    await expect(page.getByText(title, { exact: true })).toBeVisible()
    await expect.poll(() => page.evaluate(() => performance.getEntriesByName('fixture:storage-denied').length)).toBeGreaterThan(0)
    expect(await readLocalRow(page, 'missing')).toBeUndefined()
    await page.evaluate(() => sessionStorage.setItem('fixture-allow-idb', 'true'))
    await reloadDocument(page)
    await expect(page.getByText(title, { exact: true })).toBeVisible()
    await expect.poll(() => readLocalRow(page, id)).toMatchObject({ title })
    await expect.poll(async () => (await measureEntries(page, 'checksum-match')).length).toBeGreaterThan(0)
    await page.evaluate(() => sessionStorage.setItem('fixture-deny-write', 'true'))
    const updated = 'Network fallback after quota failure'
    expect((await request.patch(`/api/issues/${id}`, { headers, data: { title: updated } })).ok()).toBe(true)
    await expect.poll(() => page.evaluate(() => performance.getEntriesByName('fixture:quota-exceeded').length)).toBeGreaterThan(0)
    await expect.poll(() => page.evaluate(async () => (await indexedDB.databases()).some(db => db.name?.startsWith('flow-ls:')))).toBe(false)
    await expect(page.getByText(updated, { exact: true })).toBeVisible()
  })
})

test.describe('server count checks', () => {
  test.use({ workspaceNamePrefix: 'Count' })

  test('picks up a missing delta even when the stored checksum agrees with the mirror', async ({ page, request, workspaceKey }) => {
    const headers = { 'X-Workspace-Key': workspaceKey }
    const settings = await request.patch('/api/account/settings', { headers, data: { pulseSchedule: 'never', timezone: 'UTC' } })
    expect(settings.status()).toBe(200)
    const { teams } = await (await request.get('/api/bootstrap', { headers })).json()
    const { id } = await (await request.post('/api/issues', { headers, data: { teamId: teams[0].id, title: 'Baseline issue' } })).json()
    await page.goto(`/${workspaceKey}/team/${teams[0].key.toLowerCase()}/all`)
    await expect.poll(() => readLocalRow(page, id)).toMatchObject({ title: 'Baseline issue' })
    await expect.poll(async () => (await measureEntries(page, 'checksum-match')).length).toBeGreaterThan(0)
    await page.route(/\/api\/sync\/changes\?surface=local-store/, async route => {
      const response = await route.fetch()
      const data = await response.json()
      await route.fulfill({ response, json: { ...data, transactions: [] } })
    })
    const title = 'Issue missing from the shadow journal'
    expect((await request.post('/api/issues', { headers, data: { teamId: teams[0].id, title } })).status()).toBe(201)
    await expect(page.getByText(title, { exact: true })).toBeVisible()
    await nextDay(page)
    await expect.poll(() => measureEntries(page, 'count-mismatch')).toHaveLength(1)
    expect(await measureEntries(page, 'checksum-mismatch')).toEqual([])
    await expect.poll(() => readLocalRow(page, id)).toBeUndefined()
  })
})

import { expect, type Locator, type Page } from '@playwright/test'
import { test } from './scale-p20-fixture'

async function capture(page: Page, name: string, crop?: Locator) {
  await page.evaluate(() => document.fonts.ready)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  const options = { path: `/tmp/codex-scale-p20-enable/shots/${name}.png`, animations: 'disabled', scale: 'css' } as const
  if (crop) await crop.screenshot(options)
  else await page.screenshot(options)
}

// A controllable disconnect exercises recovery; every read uses the real API.
async function syncReads(page: Page) {
  await page.addInitScript(() => {
    const streams: WireStream[] = []
    class WireStream {
      onopen: ((event: Event) => void) | null = null
      onerror: ((event: Event) => void) | null = null
      onmessage: ((event: MessageEvent) => void) | null = null
      readyState = 0
      constructor(readonly url: string) {
        streams.push(this)
        setTimeout(() => { if (this.readyState !== 2) { this.readyState = 1; this.onopen?.(new Event('open')) } }, 0)
      }
      addEventListener() {}
      close() { this.readyState = 2 }
    }
    Object.defineProperty(window, 'EventSource', { value: WireStream, configurable: true })
    Object.assign(window, { scaleP20Disconnect: () => { for (const stream of streams) stream.onerror?.(new Event('error')) } })
  })
}

for (const flags of [false, true]) {
  for (const width of [1440, 390]) {
    test.describe(`scale-p20 flags ${flags} width ${width}`, () => {
      test.use({ scaleP20Flags: flags, viewport: { width, height: width === 1440 ? 900 : 844 }, isMobile: width < 500, hasTouch: width < 500 })
      test('documents, history, deleted direct links, restore, people and mentions', async ({ page, request, workspaceKey }) => {
        test.setTimeout(90_000)
        const headers = { 'X-Workspace-Key': workspaceKey }
        const suffix = `${flags ? 'on' : 'off'}-${width}`
        expect((await request.patch('/api/account/settings', { headers, data: { language: width < 500 ? 'zh-CN' : 'en-US', interfaceTheme: width < 500 ? 'Light' : 'Dark' } })).ok()).toBe(true)
        let core = await (await request.get('/api/bootstrap', { headers })).json()
        const profile = await request.patch('/api/account/profile', { headers, data: { displayName: width < 500 ? '王明' : 'Alexandra Montgomery', username: 'p20-viewer' } })
        expect(profile.ok(), await profile.text()).toBe(true)
        core = await (await request.get('/api/bootstrap', { headers })).json()
        const team = core.teams[0]
        const response = await request.post('/api/documents', { headers, data: { title: 'P20 document', content: 'P20 saved body survives metadata edits.', teamIds: [team.id] } })
        expect(response.status()).toBe(201)
        const doc = await response.json()
        expect((await request.patch(`/api/documents/${doc.id}`, { headers, data: { content: 'P20 current body survives metadata edits.' } })).ok()).toBe(true)
        let manifests = 0, records = 0
        page.on('request', request => {
          const path = new URL(request.url()).pathname
          if (path === '/api/sync/manifest') manifests++
          if (path === '/api/sync/records') records++
        })
        if (flags) {
          await page.goto(`/${workspaceKey}/document/${doc.slugId}`)
          await expect(page.locator('.document-page')).toContainText('P20 current body survives metadata edits.', { timeout: 15_000 })
          await capture(page, `compatibility-${suffix}`, page.locator('.document-page'))
          await syncReads(page)
        }
        await page.goto(`/${workspaceKey}/document/${doc.slugId}`)
        await expect(page.locator('.document-page')).toContainText('P20 current body survives metadata edits.', { timeout: 15_000 })
        const title = page.locator('.document-title')
        await title.fill('P')
        await title.fill('P20 document with a long realistic title for workspace review and release planning')
        await title.fill('P20 renamed document')
        await title.press('Tab')
        await expect.poll(async () => (await (await request.get(`/api/documents/${doc.id}`, { headers })).json()).title).toBe('P20 renamed document')
        expect((await (await request.get(`/api/documents/${doc.id}`, { headers })).json()).content).toBe('P20 current body survives metadata edits.')
        await capture(page, `document-${suffix}`)

        await page.locator('.document-edited').click()
        const owner = page.getByRole('button', { name: /^(Change owner|更改所有者)$/ })
        await owner.hover()
        await owner.focus()
        await owner.press('Enter')
        const ownerSearch = page.getByRole('textbox', { name: /Change owner|更改所有者/ })
        const creatorName = doc.creator.displayName || doc.creator.name
        await ownerSearch.fill(creatorName)
        await expect(page.getByRole('option').filter({ hasText: creatorName })).toBeVisible()
        await capture(page, `owner-${suffix}`, ownerSearch.locator('xpath=ancestor::*[@role="dialog"][1]'))
        await page.keyboard.press('Escape')
        await page.keyboard.press('Escape')

        const subscribers = page.getByRole('button', { name: /^(Change document subscribers|更改文档订阅者)$/ })
        await subscribers.hover()
        await subscribers.focus()
        await subscribers.press('Enter')
        const subscriber = page.locator('.document-subscribers__row').first()
        await expect(subscriber).toBeVisible()
        const subscriberName = (await subscriber.getAttribute('aria-label'))!
        await page.locator('.document-subscribers__filter').fill(subscriberName)
        await expect(subscriber).toBeVisible()
        await page.locator('.document-subscribers__filter').fill('')
        if (await subscriber.getAttribute('aria-checked') !== 'true') await subscriber.click()
        await expect(subscriber).toHaveAttribute('aria-checked', 'true')
        await capture(page, `subscribers-${suffix}`, page.locator('.document-subscribers'))
        await page.keyboard.press('Escape')

        await page.locator('.document-title-actions [aria-haspopup="menu"]').click()
        await page.getByRole('menuitem', { name: /^(People with access|有权访问的成员)$/ }).click()
        await expect(page.locator('.document-access')).toBeVisible()
        await expect(page.locator('.document-access footer button').last()).toBeDisabled()
        await capture(page, `access-${suffix}`, page.locator('.document-access'))
        await page.keyboard.press('Escape')

        await page.goto(`/${workspaceKey}/document/${doc.slugId}?history`)
        await expect(page.locator('.document-history-dialog')).toBeVisible()
        await expect(page.locator('.document-history-version')).not.toHaveCount(0)
        await expect(page.locator('.document-history-restore')).toBeDisabled()
        await capture(page, `history-${suffix}`)
        await page.keyboard.press('Escape')

        await page.goto(`/${workspaceKey}/team/${team.key}/documents`)
        await expect(page.locator('.team-documents')).toContainText('P20 renamed document')
        expect(flags ? records > 0 : records === 0).toBe(true)
        const row = page.locator('[data-document-id]').filter({ hasText: 'P20 renamed document' })
        await row.hover()
        await row.focus()
        await page.keyboard.press('x')
        await expect(row).toHaveAttribute('data-selected', 'true')
        await capture(page, `directory-${suffix}`)

        await page.goto(`/${workspaceKey}/document/${doc.slugId}`)
        const editor = page.locator('.document-editor [contenteditable="true"]')
        await editor.click()
        await page.keyboard.press('ControlOrMeta+End')
        await page.keyboard.type(' @')
        await expect(page.locator('.description-mention-menu')).toBeVisible()
        await capture(page, `mentions-${suffix}`)
        await page.keyboard.press('Escape')

        expect((await request.delete(`/api/documents/${doc.id}`, { headers })).ok()).toBe(true)
        const deletedSnapshot = await (await request.get('/api/bootstrap', { headers })).json()
        const deletedEntry = deletedSnapshot.trash.find((entry: { resourceId: string }) => entry.resourceId === doc.id)
        const deletedDocument = typeof deletedEntry.payload === 'string' ? JSON.parse(deletedEntry.payload) : deletedEntry.payload
        await page.goto(`/${workspaceKey}/document/${deletedDocument.slugId}`)
        await expect(page.locator('.document-page.is-deleted')).toContainText('P20 current body survives metadata edits.')
        const snapshot = await (await request.get('/api/bootstrap', { headers })).json()
        const trash = snapshot.trash.find((entry: { resourceId: string }) => entry.resourceId === doc.id)
        await page.locator('.document-title-actions [aria-haspopup="menu"]').click()
        await capture(page, `trash-${suffix}`)
        const restored = page.waitForResponse(response => new URL(response.url()).pathname === `/api/trash/${trash.id}/restore`)
        await page.getByRole('menuitem', { name: /^(Restore document|恢复文档)$/ }).click()
        expect((await restored).ok()).toBe(true)
        await expect(page.locator('.document-page:not(.is-deleted)')).toContainText('P20 current body survives metadata edits.')

        const memberIdentity = await request.patch(`/api/workspaces/${workspaceKey}/members/${core.viewer.id}`, { headers, data: { displayName: width < 500 ? '王明' : 'Alexandra Montgomery', email: `p20-${workspaceKey}@example.test` } })
        expect(memberIdentity.ok(), await memberIdentity.text()).toBe(true)
        core = await (await request.get('/api/bootstrap', { headers })).json()
        const memberName = core.members.find((member: { user: { id: string } }) => member.user.id === core.viewer.id).user.displayName
        await page.goto(`/${workspaceKey}/settings/members`)
        await expect(page.locator('.settings-workspace-members')).toBeVisible()
        const search = page.locator('.settings-members-toolbar input').first()
        await search.fill(memberName.slice(0, 2))
        await expect(page.locator('.settings-members-directory')).toContainText(memberName)
        await capture(page, `people-${suffix}`)
        expect(flags ? manifests > 0 : manifests === 0).toBe(true)
        expect((await request.patch('/api/account/settings', { headers, data: { language: width < 500 ? 'en-US' : 'zh-CN', interfaceTheme: width < 500 ? 'Dark' : 'Light' } })).ok()).toBe(true)
        await page.reload()
        await expect(page.locator('.settings-workspace-members')).toBeVisible()
        if (width < 500) {
          await page.setViewportSize({ width: 375, height: 812 })
          expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
        }
      })

      if (flags) test('a queued save response cannot revive a document after sync recovery', async ({ page, request, workspaceKey }) => {
        const headers = { 'X-Workspace-Key': workspaceKey }
        const core = await (await request.get('/api/bootstrap', { headers })).json()
        const created = await request.post('/api/documents', { headers, data: { title: 'Queued save', content: 'Body kept in trash', teamIds: [core.teams[0].id] } })
        expect(created.status()).toBe(201)
        const doc = await created.json()
        await syncReads(page)
        let oldReplica = false
        await page.route('**/api/sync/changes**', route => oldReplica ? route.fulfill({ status: 501, json: {} }) : route.fallback())
        let ready!: () => void, release!: () => void, delivered!: () => void
        const sent = new Promise<void>(resolve => { ready = resolve })
        const held = new Promise<void>(resolve => { release = resolve })
        const completed = new Promise<void>(resolve => { delivered = resolve })
        await page.route(url => url.pathname === `/api/documents/${doc.id}`, async route => {
          if (route.request().method() !== 'PATCH') return route.continue()
          const response = await route.fetch()
          ready()
          await held
          try { await route.fulfill({ response }) } finally { delivered() }
        })
        await page.goto(`/${workspaceKey}/document/${doc.slugId}`)
        await expect(page.locator('.document-page')).toContainText('Body kept in trash', { timeout: 15_000 })
        await page.locator('.document-title').fill('Queued response')
        await page.locator('.document-title').press('Tab')
        await sent
        try {
          oldReplica = true
          expect((await request.delete(`/api/documents/${doc.id}`, { headers })).ok()).toBe(true)
          await page.evaluate(() => (window as unknown as { scaleP20Disconnect: () => void }).scaleP20Disconnect())
          await expect(page.locator('.document-page.is-deleted')).toBeVisible({ timeout: 15000 })
        } finally { release() }
        await completed
        await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
        await expect(page.locator('.document-page.is-deleted')).toContainText('Body kept in trash')
        await expect(page.locator('.document-page [contenteditable="true"]')).toHaveCount(0)
      })

      if (flags && width === 1440) test('a failed directory handoff exposes retry and recovers', async ({ page, request, workspaceKey }) => {
        const headers = { 'X-Workspace-Key': workspaceKey }
        const core = await (await request.get('/api/bootstrap', { headers })).json()
        const created = await request.post('/api/documents', { headers, data: { title: 'Retry document', content: 'Retry body', teamIds: [core.teams[0].id] } })
        expect(created.status()).toBe(201)
        const doc = await created.json()
        await syncReads(page)
        let fail = false
        await page.route('**/api/sync/manifest**', route => fail ? route.fulfill({ status: 503, json: {} }) : route.fallback())
        await page.goto(`/${workspaceKey}/document/${doc.slugId}`)
        await expect(page.locator('.document-page')).toContainText('Retry body', { timeout: 15_000 })
        fail = true
        await page.locator('.document-breadcrumb-documents').click()
        await expect(page.getByText('Unable to load workspace', { exact: true })).toBeVisible({ timeout: 15000 })
        fail = false
        await page.getByRole('button', { name: 'Retry', exact: true }).click()
        await expect(page.locator('.team-documents')).toContainText('Retry document')
      })
    })
  }
}

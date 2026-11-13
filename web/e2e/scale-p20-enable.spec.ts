import { expect, type Page, type Locator } from '@playwright/test'
import { writeFileSync } from 'node:fs'
import pixelmatch from 'pixelmatch'
import { PNG } from 'pngjs'
import { test } from './scale-p20-fixture'

test.use({ launchOptions: { args: ['--disable-gpu', '--num-raster-threads=1', '--disable-partial-raster'] } })

for (const width of [1440, 390]) {
  test.describe(`scale-p20 enable ${width}`, () => {
    const height = width === 1440 ? 900 : 844
    test.use({ scaleP20Flags: true, viewport: { width, height }, screen: { width, height }, deviceScaleFactor: 1, isMobile: width < 500, hasTouch: width < 500 })
    test('real record transports preserve screens and document, trash, people and picker behavior', async ({ page, request, workspaceKey, scaleP20URL, scaleP20LegacyURL }) => {
      test.setTimeout(180_000)
      const headers = { 'X-Workspace-Key': workspaceKey }
      const language = width === 1440 ? 'en-US' : 'zh-CN'
      expect((await request.patch('/api/account/settings', { headers, data: { language, interfaceTheme: width === 1440 ? 'Dark' : 'Light' } })).ok()).toBe(true)
      expect((await request.patch('/api/account/profile', { headers, data: { displayName: '王明', username: 'p20-viewer' } })).ok()).toBe(true)
      let core = await (await request.get('/api/bootstrap', { headers })).json()
      expect((await request.patch(`/api/workspaces/${workspaceKey}/members/${core.viewer.id}`, { headers, data: { displayName: '王明', email: `p20-${workspaceKey}@example.test` } })).ok()).toBe(true)
      core = await (await request.get('/api/bootstrap', { headers })).json()
      const created = await request.post('/api/documents', { headers, data: { title: 'P20 review document', content: 'The saved current body.', teamIds: [core.teams[0].id], subscriberIds: [core.viewer.id] } })
      expect(created.status()).toBe(201)
      const doc = await created.json()
      const creatorName = doc.creator.displayName || doc.creator.name
      const imported = await request.post(`/api/workspace/documents/${doc.id}/revisions/import`, { headers, data: { title: doc.title, content: 'An earlier saved body.', createdAt: '2026-10-01T12:00:00Z' } })
      expect(imported.ok(), await imported.text()).toBe(true)
      const historical = (await imported.json()).revision.id as string
      const legacy = await page.context().newPage()
      const pages = [{ page: legacy, url: scaleP20LegacyURL, mode: 'off' }, { page, url: scaleP20URL, mode: 'on' }]
      const requests: string[] = []
      page.on('request', req => { if (new URL(req.url()).pathname.startsWith('/api/')) requests.push(req.url()) })
      for (const entry of pages) await entry.page.clock.setFixedTime(new Date('2026-11-12T18:00:00Z'))
      const pair = async (name: string, path: string, prepare?: (page: Page) => Promise<Locator | void>) => {
        const shots: Buffer[] = [], states: unknown[] = []
        for (const entry of pages) {
          await entry.page.bringToFront()
          await entry.page.goto(`${entry.url}/${workspaceKey}/${path}`)
          const crop = await prepare?.(entry.page)
          await entry.page.setViewportSize({ width, height: height + 1 })
          await entry.page.setViewportSize({ width, height })
          await entry.page.evaluate(() => document.fonts.ready)
          await entry.page.evaluate(() => Promise.all(document.getAnimations().filter(animation => animation.effect?.getComputedTiming().endTime !== Infinity).map(animation => animation.finished.catch(() => undefined))))
          await crop?.evaluate(element => element.scrollTo(0, 0))
          await entry.page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
          expect(await entry.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
          const metrics = await entry.page.evaluate(() => Object.fromEntries(['.workspace-avatar', '.document-subscribers', '.document-subscribers__document', '.document-subscribers__search', '.document-subscribers__row', '.document-history-title', '.document-history-shell-panel', '.document-history-panel', '.document-history-main', '.document-history-scroll', '.document-history-pill'].map(selector => {
            const element = document.querySelector(selector)
            if (!element) return [selector, null]
            const style = getComputedStyle(element)
            return [selector, { rect: element.getBoundingClientRect().toJSON(), styles: Object.fromEntries(['padding', 'border', 'outline', 'transform', 'translate', 'scale', 'zoom', 'font', 'background', 'border-radius'].map(property => [property, style.getPropertyValue(property)])), parent: element.parentElement?.getAttribute('style') }]
          })))
          writeFileSync(`/tmp/codex-scale-p20-enable/shots/enable-${width}-${name}-${entry.mode}.json`, JSON.stringify(metrics, null, 2))
          states.push(metrics)
          const options = { path: `/tmp/codex-scale-p20-enable/shots/enable-${width}-${name}-${entry.mode}.png`, animations: 'allow', caret: 'hide', scale: 'css' } as const
          shots.push(crop ? await crop.screenshot(options) : await entry.page.screenshot(options))
        }
        expect(states[1], `${name}: flag-on geometry or styles differ from flag-off`).toEqual(states[0])
        const off = PNG.sync.read(shots[0]), on = PNG.sync.read(shots[1])
        expect([on.width, on.height]).toEqual([off.width, off.height])
        const changed = pixelmatch(off.data, on.data, null, off.width, off.height, { threshold: 0 })
        const raw = pixelmatch(off.data, on.data, null, off.width, off.height, { threshold: 0, includeAA: true })
        writeFileSync(`/tmp/codex-scale-p20-enable/shots/enable-${width}-${name}-comparison.json`, JSON.stringify({ changedPixels: changed, rawPixels: raw, antialiasPixels: raw - changed }))
        expect(changed, `${name}: flag-on pixels differ from flag-off`).toBe(0)
        expect(raw, `${name}: flag-on raw pixels differ from flag-off`).toBe(0)
      }
      try {
        await pair('document', `document/${doc.slugId}`, async page => {
          await expect(page.locator('.document-page')).toContainText('The saved current body.')
        })
        await pair('owner-focus', `document/${doc.slugId}`, async page => {
          await expect(page.locator('.document-page')).toContainText('The saved current body.')
          await page.locator('.document-edited').click()
          const owner = page.getByRole('button', { name: /^(Change owner|更改所有者)$/ })
          await owner.hover(); await owner.focus(); await owner.press('Enter')
          const search = page.getByRole('textbox', { name: /Change owner|更改所有者/ })
          await search.fill(creatorName)
          await expect(page.getByRole('option').filter({ hasText: creatorName })).toBeVisible()
          return search.locator('xpath=ancestor::*[@role="dialog"][1]')
        })
        await pair('subscribers-selected', `document/${doc.slugId}`, async page => {
          await expect(page.locator('.document-page')).toContainText('The saved current body.')
          await page.getByRole('button', { name: /^(Change document subscribers|更改文档订阅者)$/ }).click()
          const row = page.locator('.document-subscribers__row').first()
          await expect(row).toHaveAttribute('aria-checked', 'true')
          await row.hover(); await row.focus()
          return page.locator('.document-subscribers')
        })
        await pair('access-disabled', `document/${doc.slugId}`, async page => {
          await expect(page.locator('.document-page')).toContainText('The saved current body.')
          await page.locator('.document-title-actions [aria-haspopup="menu"]').click()
          await page.getByRole('menuitem', { name: /^(People with access|有权访问的成员)$/ }).click()
          await expect(page.locator('.document-access footer button').last()).toBeDisabled()
          return page.locator('.document-access')
        })
        await pair('history-current', `document/${doc.slugId}?history`, async page => {
          await expect(page.locator('.document-history-dialog')).toBeVisible()
          await expect(page.locator('.document-history-doc')).toContainText('saved current body')
          await expect(page.locator('.document-history-doc')).toContainText('earlier')
          await expect(page.locator('.document-history-restore')).toBeDisabled()
          if (width < 500) {
            await page.getByRole('combobox', { name: /Select document version|选择文档版本/ }).selectOption(historical)
            await page.getByRole('combobox', { name: /Select document version|选择文档版本/ }).selectOption('current')
          } else {
            await page.locator('.document-history-version').last().click()
            await page.locator('.document-history-version').first().click()
          }
          await expect(page.locator('.document-history-doc')).toContainText('earlier')
          await page.locator('.document-history-doc').click()
          return page.locator('.document-history-dialog')
        })
        await pair('history-selected', `document/${doc.slugId}?history`, async page => {
          await expect(page.locator('.document-history-dialog')).toBeVisible()
          if (width < 500) await page.getByRole('combobox', { name: /Select document version|选择文档版本/ }).selectOption(historical)
          else await page.locator('.document-history-version').last().click()
          await expect(page.locator('.document-history-doc')).toContainText('An earlier saved body.')
          await expect(page.locator('.document-history-restore')).toBeEnabled()
          await page.locator('.document-history-doc').click()
          return page.locator('.document-history-dialog')
        })
        await pair('directory', `team/${core.teams[0].key}/documents`, async page => {
          const row = page.locator('[data-document-id]').filter({ hasText: doc.title })
          await expect(row).toBeVisible()
          await row.hover(); await row.focus(); await page.keyboard.press('x')
          await expect(row).toHaveAttribute('data-selected', 'true')
        })
        expect((await request.delete(`/api/documents/${doc.id}`, { headers })).ok()).toBe(true)
        await pair('trash-directory', `team/${core.teams[0].key}/archive/recently-deleted-documents`, async page => {
          await expect(page.locator('.archive-doc-row')).toContainText(doc.title)
        })
        await pair('trash-direct', `document/${doc.slugId}`, async page => {
          await expect(page.locator('.document-page.is-deleted')).toContainText('The saved current body.')
          await page.locator('.document-title-actions [aria-haspopup="menu"]').click()
        })
        const trashRead = await request.get(`/api/resource-windows/trash?resourceId=${doc.id}`, { headers })
        expect(trashRead.ok()).toBe(true)
        const entry = await trashRead.json()
        expect(entry.links).toBeUndefined()
        const restored = page.waitForResponse(response => new URL(response.url()).pathname === `/api/trash/${entry.id}/restore`)
        await page.getByRole('menuitem', { name: /^(Restore document|恢复文档)$/ }).click()
        expect((await restored).ok()).toBe(true)
        await pair('restored', `document/${doc.slugId}`, async page => {
          await expect(page.locator('.document-page:not(.is-deleted)')).toContainText('The saved current body.')
        })
        for (const entry of pages) {
          await entry.page.locator('.document-title').fill('P20 metadata-only rename')
          await entry.page.locator('.document-title').press('Tab')
          await expect.poll(async () => (await (await request.get(`/api/documents/${doc.id}`, { headers })).json()).title).toBe('P20 metadata-only rename')
          expect((await (await request.get(`/api/documents/${doc.id}`, { headers })).json()).content).toBe('The saved current body.')
        }
        expect((await request.patch(`/api/workspaces/${workspaceKey}/members/${core.viewer.id}`, { headers, data: { displayName: '王明', email: `p20-${workspaceKey}@example.test` } })).ok()).toBe(true)
        const memberName = '王明'
        await pair('people-search', 'settings/members', async page => {
          await expect(page.locator('.settings-workspace-members')).toBeVisible()
          await page.locator('.settings-members-toolbar input').first().fill(memberName)
          await expect(page.locator('.settings-members-directory')).toContainText(memberName)
        })
        expect(requests.some(url => url.includes('/api/resource-windows/members'))).toBe(true)
        expect(requests.some(url => url.includes('/api/resource-windows/trash?resourceId='))).toBe(true)
        expect(requests.some(url => url.includes(`/history/${historical}`))).toBe(true)
        expect(requests.filter(url => new URL(url).pathname === '/api/bootstrap')).toEqual([])
      } finally { await legacy.close() }
    })
  })
}

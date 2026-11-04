import { expect } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import { test } from './workspace-fixture'

const shots = process.env.FLOW_ARCHIVE_SHOTS

for (const width of [1440, 390, 375]) for (const theme of ['dark', 'light'] as const) for (const locale of ['en-US', 'zh-CN']) {
  test.describe(`${width} ${theme} ${locale}`, () => {
    test.use({ viewport: { width, height: width === 1440 ? 900 : 844 }, isMobile: width < 800, hasTouch: width < 800, colorScheme: theme })

    test('requires restoration before editing archived content and properties', async ({ page, request, workspaceKey }) => {
      test.setTimeout(60000)
      const headers = { 'X-Workspace-Key': workspaceKey }
      const bootstrap = await (await request.get('/api/bootstrap', { headers })).json()
      expect((await request.patch('/api/account/settings', { headers, data: { interfaceTheme: theme === 'dark' ? 'Dark' : 'Light', language: locale } })).ok()).toBe(true)
      const project = await request.post('/api/projects', { headers, data: { name: theme === 'dark' ? '中a' : 'International release readiness and customer integrations 国际化发布准备', teamIds: [bootstrap.teams[0].id] } })
      expect(project.status()).toBe(201)
      const created = await request.post('/api/issues', { headers, data: {
        title: theme === 'dark' ? '中a' : 'International release readiness and customer integrations 国际化发布准备',
        description: 'Archived release notes 归档发布说明',
        teamId: bootstrap.teams[0].id,
        assigneeId: bootstrap.viewer.id,
        projectId: (await project.json()).id,
      } })
      expect(created.status()).toBe(201)
      const issue = await created.json()
      expect((await request.patch(`/api/issues/${issue.id}`, { headers, data: { archived: true } })).ok()).toBe(true)
      await page.route('**/api/**', async route => {
        const pathname = new URL(route.request().url()).pathname
        if (!['GET', 'PATCH'].includes(route.request().method()) || !/^\/api\/(?:bootstrap|issue-records|issues)(?:\/|$)/.test(pathname)) return route.continue()
        const response = await route.fetch()
        const body = await response.json()
        const rename = (value: unknown) => {
          if (!value || typeof value !== 'object') return
          const object = value as Record<string, unknown>
          if (object.id === bootstrap.viewer.id) object.name = object.displayName = '中 文'
          for (const child of Object.values(object)) rename(child)
        }
        rename(body)
        await route.fulfill({ response, json: body })
      })
      const writes: string[] = []
      page.on('request', request => {
        if (request.method() === 'PATCH' && new RegExp(`/api/(?:issues|issue-records)/${issue.id}$`).test(new URL(request.url()).pathname)) writes.push(request.postData() ?? '')
      })
      await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
      await expect(page.locator('html')).toHaveAttribute('data-locale', locale)
      const title = page.locator('.title-editor')
      await expect(title).toBeVisible({ timeout: 20000 })
      await expect(title).toHaveAttribute('contenteditable', 'false')
      await expect(page.locator('.description-editor')).toHaveAttribute('contenteditable', 'false')
      await page.evaluate(() => document.fonts.ready)
      const properties = page.locator(width === 1440 ? '.issue-properties-rail' : '.issue-mobile-properties')
      await expect(properties).toBeVisible()
      const priority = properties.locator('[data-property="priority"]')
      await expect(priority).toBeDisabled()
      const before = await priority.boundingBox()
      await priority.hover({ force: true })
      expect(await priority.boundingBox()).toEqual(before)
      await page.mouse.click(before!.x + before!.width / 2, before!.y + before!.height / 2)
      await expect(page.locator('.property-command-surface')).toHaveCount(0)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      const tag = `${width}-${theme}-${locale}`
      if (shots) {
        mkdirSync(shots, { recursive: true })
        await page.screenshot({ path: `${shots}/${tag}-archived.png` })
      }
      const options = page.locator('.issue-header-context > button[aria-haspopup="dialog"]')
      await page.mouse.move(0, 0)
      await expect(page.getByRole('tooltip')).toHaveCount(0)
      await options.focus()
      await page.keyboard.press('Tab')
      await page.keyboard.press('Shift+Tab')
      expect(await options.evaluate(element => element.matches(':focus-visible'))).toBe(true)
      if (shots) await options.screenshot({ path: `${shots}/${tag}-focus.png` })
      await page.keyboard.press('Enter')
      const menu = page.locator('.issue-options-popover')
      const restore = menu.locator('[cmdk-item][data-value="Restore"]')
      const due = menu.locator('[cmdk-item][data-value="Due date"]')
      await expect(restore).toBeVisible()
      await menu.evaluate(async element => {
        await Promise.all(element.getAnimations({ subtree: true }).map(animation => animation.finished))
      })
      await expect(restore).toContainText(locale === 'zh-CN' ? '恢复' : 'Restore')
      await expect(due).toHaveAttribute('aria-disabled', 'true')
      await due.hover({ force: true })
      await expect(page.locator('.issue-options-submenu[data-menu="Due date"]')).toHaveCount(0)
      if (shots) await menu.screenshot({ path: `${shots}/${tag}-open.png` })
      const rest = await restore.boundingBox()
      await restore.hover()
      await expect(restore).toHaveAttribute('aria-selected', 'true')
      expect(await restore.boundingBox()).toEqual(rest)
      if (shots) await menu.screenshot({ path: `${shots}/${tag}-hover.png` })
      await page.mouse.move(0, 0)
      await menu.locator('[cmdk-input]').focus()
      await menu.locator('[cmdk-input]').fill('Restore')
      await page.keyboard.press('ArrowDown')
      await page.keyboard.press('Home')
      await expect(restore).toHaveAttribute('aria-selected', 'true')
      if (shots) await menu.screenshot({ path: `${shots}/${tag}-keyboard.png` })
      expect(writes).toEqual([])
      await restore.click()
      await expect(title).toHaveAttribute('contenteditable', 'true')
      await expect(page.locator('.description-editor')).toHaveAttribute('contenteditable', 'true')
      await expect(priority).toBeEnabled()
      expect(writes).toHaveLength(1)
      expect(JSON.parse(writes[0]).archived).toBe(false)
      await priority.click()
      await expect(page.locator('.property-command-surface')).toBeVisible()
      await page.keyboard.press('Escape')
    })
  })
}

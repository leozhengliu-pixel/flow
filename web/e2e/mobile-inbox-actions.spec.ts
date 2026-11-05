import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import { bootstrap, preferences } from './review-findings-fixture'

// Match the strict phone gate's native scale so half-pixel borders retain their width.
test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark', deviceScaleFactor: 3, userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1', launchOptions: { args: ['--force-device-scale-factor=2'] } })

test('opens the phone notification actions sheet', async ({ page, request, workspaceKey }, testInfo) => {
  const data = await bootstrap(request, workspaceKey)
  expect(data.teams[0]).toBeTruthy()
  const teamId = data.teams[0].id
  const headers = { 'X-Workspace-Key': workspaceKey }

  for (const title of ['[mobile] Actions notification one', '[mobile] Actions notification two']) {
    const issue = await request.post('/api/issues', { headers, data: { title, description: 'Mobile inbox action row', teamId, stateId: 'state_todo' } })
    expect(issue.status()).toBe(201)
    const issueBody = await issue.json() as { id: string }
    const reminder = await request.post(`/api/issues/${issueBody.id}/reminders`, { headers, data: { remindAt: new Date(Date.now() + 300).toISOString() } })
    expect(reminder.status()).toBe(201)
  }
  await expect.poll(async () => {
    const response = await request.get('/api/notifications?limit=50', { headers })
    expect(response.status()).toBe(200)
    const data = await response.json() as { notifications: unknown[] }
    return data.notifications.length
  }).toBe(2)

  await page.goto(`/${workspaceKey}/inbox`)
  await page.locator('.flow-inbox__header button[aria-label="Notification actions"]').click()

  const near = (actual: number | undefined, expected: number) => expect(Math.abs((actual ?? Number.NaN) - expected)).toBeLessThanOrEqual(1)
  const surface = page.locator('.phone-menu-surface.inbox-actions-phone-menu')
  await expect(surface).toBeVisible()
  await surface.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
  await expect(surface).toHaveCSS('border-left-width', '0.5px')
  await expect(surface).toHaveCSS('border-right-width', '0.5px')
  near((await surface.boundingBox())?.x, 16)
  near((await surface.boundingBox())?.y, 110)
  near((await surface.boundingBox())?.width, 358)
  near((await surface.boundingBox())?.height, 254)

  const search = surface.locator('input')
  near((await search.boundingBox())?.x, 22.5)
  near((await search.boundingBox())?.y, 116.5)
  expect((await search.boundingBox())?.width).toBe(346)
  near((await search.boundingBox())?.height, 40)

  const rows = surface.locator('.inbox-actions-phone-menu__row')
  await expect(rows).toHaveCount(4)
  await expect(rows.locator('.inbox-actions-phone-menu__label')).toHaveText(['Delete all', 'Delete all read', 'Delete all completed', 'Go to settings'])
  near((await rows.nth(0).boundingBox())?.y, 162.5)
  near((await rows.nth(0).boundingBox())?.height, 46)
  expect((await rows.nth(0).boundingBox())?.width).toBe(346)
  near((await rows.nth(0).locator('.inbox-actions-phone-menu__label').boundingBox())?.x, 58.5)
  near((await rows.nth(3).boundingBox())?.y, 312.5)

  const divider = surface.locator('.inbox-actions-phone-menu__separator')
  near((await divider.boundingBox())?.x, 16.5)
  near((await divider.boundingBox())?.y, 300.5)
  expect((await divider.boundingBox())?.width).toBe(358)
  near((await divider.boundingBox())?.height, 12)

  await expect(surface.getByRole('menuitem', { name: 'Go to settings' })).toBeVisible()

  const geometry = await page.evaluate(() => ({ width: innerWidth, scrollWidth: document.documentElement.scrollWidth }))
  expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.width)

  if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/inbox-actions-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
})

for (const width of [375, 390]) for (const theme of ['Dark', 'Light'] as const) for (const locale of ['en-US', 'zh-CN']) {
  test(`keeps phone inbox glyphs in their content inset ${width} ${theme} ${locale}`, async ({ page, request, workspaceKey }) => {
    await page.setViewportSize({ width, height: width === 375 ? 667 : 844 })
    await preferences(request, workspaceKey, theme, locale)
    await page.goto(`/${workspaceKey}/inbox`)
    const header = page.locator('.flow-inbox__header')
    for (const [trigger, surfaceSelector, iconSelector] of [
      ['[data-inbox-filter-trigger]', '.inbox-filter-phone-properties', '[cmdk-item] > svg'],
      [`button[aria-label="${locale === 'zh-CN' ? '通知操作' : 'Notification actions'}"]`, '.inbox-actions-phone-menu', '[data-testid="inbox-actions-delete-all"] .inbox-actions-phone-menu__icon > svg'],
    ]) {
      await header.locator(trigger).click()
      const surface = page.locator(surfaceSelector)
      await expect(surface).toBeVisible()
      await surface.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
      const icon = surface.locator(iconSelector).first()
      await expect.poll(async () => {
        const box = (await icon.boundingBox())!
        return { x: box.x, width: box.width, height: box.height }
      }).toEqual({ x: 34.5, width: 16, height: 16 })
      const centerOffset = await icon.evaluate(element => {
        const row = element.closest('[cmdk-item], [data-testid="inbox-actions-delete-all"]')!
        const glyph = element.getBoundingClientRect(), bounds = row.getBoundingClientRect()
        return glyph.y + glyph.height / 2 - bounds.y - bounds.height / 2
      })
      expect(centerOffset).toBe(0)
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
      if (process.env.FLOW_REVIEW_SHOTS) await surface.screenshot({ path: `${process.env.FLOW_REVIEW_SHOTS}/inbox-${surfaceSelector.slice(1)}-${width}-${theme}-${locale}.png`, animations: 'disabled' })
      await page.keyboard.press('Escape')
      await expect(surface).toBeHidden()
    }
  })
}

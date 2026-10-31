import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.use({ viewport: { width: 1440, height: 900 }, isMobile: false, hasTouch: false })

test.beforeEach(async ({ request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const settings = await request.patch('/api/workspace/preferences', {
    headers,
    data: { welcomeMessageTitle: 'Welcome to Flow', welcomeMessage: 'Watch an introductory video and access a list of resources below.', welcomeMessageEnabled: true },
  })
  expect(settings.ok()).toBeTruthy()
  expect((await request.post('/api/workspace/welcome-message/test', { headers })).ok()).toBeTruthy()
})

test('row menu fits its labels and typed snooze persists the chosen time', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/inbox`)
  const row = page.locator('.flow-inbox-row').first()
  await row.click({ button: 'right', position: { x: 154, y: 28 } })
  const menu = page.getByRole('menu', { name: 'Notification actions' })
  await expect(menu).toBeVisible()
  await expect.poll(async () => Math.round((await menu.boundingBox())!.width)).toBe(190)
  const label = menu.getByText('Delete notification', { exact: true })
  expect(await label.evaluate(e => e.scrollWidth <= e.clientWidth)).toBe(true)
  await menu.getByRole('menuitem', { name: /Snooze/ }).hover()
  const snooze = page.locator('.flow-inbox-snooze-menu')
  await expect.poll(async () => Math.round((await snooze.boundingBox())!.width)).toBe(318)
  await expect(snooze.locator('time')).toHaveCount(5)
  await expect(snooze.getByRole('menuitem', { name: 'Custom…' }).locator('svg')).toHaveCount(1)
  const mutation = page.waitForResponse(r => r.request().method() === 'PATCH' && /\/api\/notifications\//.test(r.url()))
  const start = Date.now()
  await snooze.getByRole('textbox', { name: 'Snooze time' }).fill('in 3 hours')
  await snooze.getByRole('textbox', { name: 'Snooze time' }).press('Enter')
  const response = await mutation
  expect(response.ok()).toBeTruthy()
  const saved = await response.json() as { snoozedUntil: string }
  expect(Math.abs(Date.parse(saved.snoozedUntil) - start - 3 * 60 * 60 * 1000)).toBeLessThan(10_000)
  await expect(row).toHaveCount(0)
  await page.reload()
  await expect(page.locator('.flow-inbox-row')).toHaveCount(0)
})

test('H opens the same complete menu and the three-hour preset snoozes', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/inbox`)
  const row = page.locator('.flow-inbox-row').first()
  await row.focus()
  await page.keyboard.press('h')
  const snooze = page.getByRole('menu', { name: 'Snooze notification' })
  await expect(snooze.getByRole('textbox', { name: 'Snooze time' })).toBeVisible()
  await expect(snooze.getByRole('menuitem')).toHaveCount(6)
  await expect(snooze.locator('time')).toHaveCount(5)
  await expect(snooze.getByRole('menuitem', { name: 'Custom…' }).locator('svg')).toHaveCount(1)
  await expect.poll(async () => Math.round((await page.locator('.flow-inbox-snooze-menu').boundingBox())!.width)).toBe(318)
  const mutation = page.waitForResponse(r => r.request().method() === 'PATCH' && /\/api\/notifications\//.test(r.url()))
  const start = Date.now()
  await snooze.getByRole('menuitem', { name: 'In 3 hours', exact: true }).click()
  const response = await mutation
  expect(response.ok()).toBeTruthy()
  const saved = await response.json() as { snoozedUntil: string }
  expect(Math.abs(Date.parse(saved.snoozedUntil) - start - 3 * 60 * 60 * 1000)).toBeLessThan(10_000)
  await expect(row).toHaveCount(0)
})

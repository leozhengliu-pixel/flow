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
  await page.getByRole('button', { name: 'Notification actions', exact: true }).click()
  const shortcut = page.locator('.flow-inbox-menu--actions kbd')
  await expect(shortcut.locator('span')).toHaveCount(2)
  expect(await shortcut.evaluate(e => getComputedStyle(e).paddingLeft)).toBe('0px')
  await page.keyboard.press('Escape')
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
  const parentRect = (await menu.boundingBox())!
  const snoozeRect = (await snooze.boundingBox())!
  expect(Math.abs(snoozeRect.x - parentRect.x - parentRect.width + 1.5)).toBeLessThanOrEqual(1)
  expect(Math.abs(snoozeRect.y - parentRect.y - 27)).toBeLessThanOrEqual(1)
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

test('welcome document keeps its measured layout and Flow resources in view', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/inbox`)
  await page.getByText('Welcome to Flow', { exact: true }).click()
  const mark = page.locator('.flow-inbox-welcome__mark')
  const title = page.getByRole('heading', { name: 'Welcome to Flow' })
  const video = page.locator('.flow-inbox-welcome__video')
  await expect(title).toBeVisible()
  for (const [element, expected] of [[mark, { x: 608.5, y: 108.5, width: 44, height: 44 }], [title, { x: 608.5, y: 176.5, width: 759, height: 46 }], [video, { x: 608.5, y: 300.5, width: 759, height: 426.94 }]] as const) {
    const rect = (await element.boundingBox())!
    for (const dimension of ['x', 'y', 'width', 'height'] as const) expect(Math.abs(rect[dimension] - expected[dimension])).toBeLessThanOrEqual(1)
  }
  await expect(page.getByText('Introductory video unavailable')).toBeVisible()
  const docs = page.getByRole('link', { name: 'Flow documentation' })
  const support = page.getByRole('link', { name: 'Community and support' })
  await expect(docs).toHaveAttribute('href', 'https://github.com/leozhengliu-pixel/flow/tree/main/docs')
  await expect(support).toHaveAttribute('href', 'https://github.com/leozhengliu-pixel/flow/issues')
  const resources = (await page.getByRole('heading', { name: 'Resources' }).boundingBox())!
  expect(resources.y).toBeLessThan(760)
  expect((await support.boundingBox())!.y).toBeLessThan(850)
  await expect(page.getByText('Issue is no longer available')).toHaveCount(0)
})

test('unread-empty state uses the measured ghost action without extra copy', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/inbox`)
  const markedRead = page.waitForResponse(r => r.request().method() === 'PATCH' && /\/api\/notifications\//.test(r.url()))
  await page.getByText('Welcome to Flow', { exact: true }).click()
  await markedRead
  await page.goto(`/${workspaceKey}/inbox`)
  await page.getByRole('button', { name: 'Show unreads only', exact: true }).click()
  const title = page.getByText('No unreads', { exact: true })
  const action = page.getByRole('button', { name: 'Show all notifications', exact: true })
  await expect(title).toBeVisible()
  await expect(action).toBeVisible()
  expect(Math.abs((await title.boundingBox())!.y - 420.75)).toBeLessThanOrEqual(1)
  expect(Math.abs((await action.boundingBox())!.y - 452.25)).toBeLessThanOrEqual(1)
  expect(await action.evaluate(e => getComputedStyle(e).backgroundColor)).toMatch(/rgba\(0, 0, 0, 0\)|\/ 0\)$/)
  expect(await action.evaluate(e => getComputedStyle(e).fontWeight)).toBe('500')
  await expect(page.getByText('All caught up', { exact: true })).toHaveCount(0)
  await action.click()
  await expect(page.locator('.flow-inbox-row')).toHaveCount(1)
})

test('Pulse empty state has left-aligned content and a compact action row', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/pulse/following`)
  const body = page.locator('.pulse-empty-body')
  await expect(body).toBeVisible()
  const rect = (await body.boundingBox())!
  expect(Math.abs(rect.x - 668)).toBeLessThanOrEqual(1)
  expect(Math.abs(rect.y - 368.05)).toBeLessThanOrEqual(1)
  expect(Math.abs(rect.height - 223.4)).toBeLessThanOrEqual(1)
  await expect(body).toContainText('Updates from initiatives and projects that you’re a part of or subscribed to will show here.')
  expect(await body.evaluate(e => getComputedStyle(e).textAlign)).toBe('left')
  await expect(body.getByRole('link', { name: 'Documentation' })).toHaveAttribute('href', /\/flow\/.*\/docs\/pulse\.md$/)
  await body.getByRole('button', { name: 'New update' }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
})

test('Favorite and Copy remain available outside the compact row menu', async ({ page, request, context, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const boot = await (await request.get(`/api/bootstrap?workspace=${workspaceKey}`, { headers })).json() as { teams: Array<{ id: string }> }
  const created = await request.post('/api/issues', { headers, data: { title: '[audit] Inbox action access', teamId: boot.teams[0].id, stateId: 'state_todo' } })
  expect(created.status()).toBe(201)
  const issue = await created.json() as { id: string; identifier: string }
  expect((await request.post(`/api/issues/${issue.id}/reminders`, { headers, data: { remindAt: new Date(Date.now() + 300).toISOString() } })).status()).toBe(201)
  await expect.poll(async () => {
    const response = await request.get('/api/notifications', { headers })
    return (await response.json() as { notifications: Array<{ issueId?: string }> }).notifications.some(n => n.issueId === issue.id)
  }).toBe(true)
  await page.goto(`/${workspaceKey}/inbox`)
  const row = page.locator('.flow-inbox-row').filter({ hasText: '[audit] Inbox action access' })
  await row.focus()
  const favoriteMutation = page.waitForResponse(r => r.request().method() === 'PATCH' && /\/api\/notifications\//.test(r.url()))
  await page.keyboard.press('Alt+f')
  expect((await (await favoriteMutation).json() as { favorite: boolean }).favorite).toBe(true)
  await row.click()
  const favorite = page.getByRole('button', { name: 'Remove from favorites', exact: true })
  await expect(favorite).toBeVisible()
  await favorite.click()
  await expect(page.getByRole('button', { name: 'Add to favorites', exact: true })).toBeVisible()
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.getByRole('button', { name: 'Issue options', exact: true }).click()
  await page.getByRole('option', { name: 'Copy', exact: true }).click()
  await page.getByRole('option', { name: /Copy ID/ }).click()
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(issue.identifier)
  await page.getByRole('button', { name: 'Issue options', exact: true }).click()
  await page.getByRole('option', { name: 'Copy', exact: true }).click()
  await page.getByRole('option', { name: /Copy URL/ }).click()
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toContain(`/${workspaceKey}/issue/`)
})

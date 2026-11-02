import { expect } from '@playwright/test'
import { test, bootstrap, capture } from './review-findings-fixture'

test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })
test('packs searched phone filter results directly below the input', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/inbox`)
  await page.locator('.flow-inbox__header-controls button[aria-label="Add filter"]').click()
  const menu = page.locator('.inbox-filter-phone-properties')
  await menu.getByRole('combobox').fill('Project')
  const item = menu.getByRole('option', { name: 'Project', exact: true })
  await expect(menu.locator('[data-inbox-filter-shortcut]').filter({ hasText: 'No project' })).toHaveCount(1)
  const input = (await menu.getByRole('combobox').boundingBox())!
  const box = (await item.boundingBox())!
  expect(box.y - input.y - input.height).toBeLessThanOrEqual(12)
  const last = (await menu.getByRole('option').last().boundingBox())!
  const panel = (await menu.boundingBox())!
  expect(panel.y + panel.height - last.y - last.height).toBeLessThanOrEqual(8)
  await capture(menu, 'flow-inbox-filter-search')
})

test('keeps two Chinese initials inside a searched person avatar', async ({ page, request, workspaceKey }) => {
  const data = await bootstrap(request, workspaceKey)
  const headers = { 'X-Workspace-Key': workspaceKey }
  const response = await request.post('/api/issues', { headers, data: { title: 'Person search fixture', teamId: data.teams[0].id, stateId: 'state_todo' } })
  expect(response.status()).toBe(201)
  const issue = await response.json() as { id: string }
  expect((await request.post(`/api/issues/${issue.id}/reminders`, { headers, data: { remindAt: new Date(Date.now() + 300).toISOString() } })).ok()).toBe(true)
  await expect.poll(async () => (await (await request.get('/api/notifications?limit=50', { headers })).json()).notifications.length).toBeGreaterThan(0)
  await page.route('**/api/bootstrap*', async route => {
    const response = await route.fetch()
    const data = await response.json()
    data.users = data.users.map((user: { id: string }) => user.id === data.viewer.id ? { ...user, displayName: '陈 王', avatarUrl: '' } : user)
    await route.fulfill({ response, json: data })
  })
  await page.goto(`/${workspaceKey}/inbox`)
  await page.locator('.flow-inbox__header-controls button[aria-label="Add filter"]').click()
  const menu = page.locator('.inbox-filter-phone-properties')
  await menu.getByRole('combobox').fill('陈')
  const item = menu.locator('[data-inbox-filter-shortcut]').filter({ hasText: '陈 王' })
  await expect(item).toBeVisible()
  const avatar = item.locator('[data-script=cjk]')
  expect(await avatar.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await capture(menu, 'flow-inbox-filter-cjk-shortcut')
})

import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

const base = process.env.FLOW_MOBILE_BASE_URL ?? 'http://127.0.0.1:4173'
const userAgent = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1'

async function seedWorkspace(request: APIRequestContext, workspaceKey: string) {
  const created = await request.post(`${base}/api/workspaces`, { data: { name: 'Search date filter', urlKey: workspaceKey, region: 'us' } })
  expect(created.status()).toBe(201)
  const workspace = await created.json() as { teams: Array<{ id: string }> }
  const issue = await request.post(`${base}/api/issues`, { headers: { 'X-Flow-Workspace': workspaceKey }, data: { title: '[e2e] Dated result', teamId: workspace.teams[0].id } })
  expect(issue.status()).toBe(201)
  const record = await issue.json() as { id: string }
  const recent = await request.post(`${base}/api/recent`, { headers: { 'X-Flow-Workspace': workspaceKey }, data: { type: 'issue', id: record.id } })
  expect(recent.ok()).toBe(true)
}

async function applyDateFilter(page: Page, workspaceKey: string) {
  const searches: string[] = []
  page.on('request', request => { if (request.url().includes('/api/search?')) searches.push(request.url()) })
  await page.goto(`${base}/${workspaceKey}/search`)
  await expect(page.locator('a[data-result-index]').first()).toBeVisible()
  await page.getByRole('button', { name: 'Add filter', exact: true }).click()
  await page.getByRole('menuitem', { name: /Updated date/ }).click()
  await page.getByRole('button', { name: 'Updated date', exact: true }).click()
  await expect(page.locator('.date-time-popover')).toBeVisible()

  const now = new Date()
  const nextMonth = new Date(now.getFullYear(), now.getMonth() + 1, 1)
  const nextMonthValue = nextMonth.toLocaleDateString('en-CA')
  await page.getByRole('button', { name: 'Next month', exact: true }).click()
  await page.getByRole('button', { name: nextMonth.toLocaleDateString('en-US'), exact: true }).click()

  const chips = page.locator('.workspace-search-filter-chips')
  await expect(chips).toContainText('Updated date')
  await expect(chips).toContainText('after')
  await expect(chips).toContainText(nextMonthValue)
  await expect(page.locator('a[data-result-index]')).toHaveCount(0)
  await expect.poll(() => searches.length).toBeGreaterThan(0)
  const filter = JSON.parse(new URL(searches.at(-1)!).searchParams.get('filter')!)
  expect(filter).toEqual({ and: [{ field: 'updatedAt', operator: 'after', values: [nextMonthValue] }] })
  expect(page.url()).toContain('filters=')
}

test.describe('workspace search date filter', () => {
  test.describe('desktop', () => {
    test.use({ viewport: { width: 1440, height: 900 } })
    test('picks a date and applies the filter', async ({ page, request }, testInfo) => {
      test.skip(testInfo.project.name !== 'chromium', 'Desktop geometry runs once')
      const workspaceKey = `search-date-desktop-${testInfo.retry}`
      await seedWorkspace(request, workspaceKey)
      await applyDateFilter(page, workspaceKey)
    })
  })

  test.describe('phone', () => {
    test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent })
    test('picks a date and applies the filter', async ({ page, request }, testInfo) => {
      test.skip(testInfo.project.name !== 'mobile-chromium', 'Phone geometry runs once')
      const workspaceKey = `search-date-phone-${testInfo.retry}`
      await seedWorkspace(request, workspaceKey)
      await applyDateFilter(page, workspaceKey)
    })
  })
})

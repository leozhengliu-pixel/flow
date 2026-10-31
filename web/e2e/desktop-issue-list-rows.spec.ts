import { test, expect } from './issue-list-fixture'
import type { BootstrapData, SavedView } from '../src/types/flow'

test.skip(({ isMobile }) => isMobile, 'Desktop issue list metrics')
test.use({ viewport: { width: 1440, height: 900 } })

test('keeps compact rows, a dim disclosure and nested elbow connectors', async ({ page, request, workspaceKey, issueList }) => {
  const child = issueList.issues[1]
  const headers = { 'X-Workspace-Key': workspaceKey }
  const enabled = await request.patch(`/api/teams/${issueList.data.teams[0].id}/cycle-settings`, { headers, data: { enabled: true, autoCreate: true } })
  expect(enabled.ok()).toBeTruthy()
  const bootstrap = await request.get('/api/bootstrap', { headers })
  const cycle = ((await bootstrap.json()) as BootstrapData).cycles[0]
  expect(cycle).toBeTruthy()
  const assigned = await request.patch(`/api/issues/${issueList.issues[0].id}`, { headers, data: { cycleId: cycle.id } })
  expect(assigned.ok()).toBeTruthy()
  const response = await request.patch(`/api/issues/${child.id}`, {
    headers: { 'X-Workspace-Key': workspaceKey },
    data: { parentId: issueList.issues[0].id },
  })
  expect(response.ok()).toBeTruthy()
  await page.goto(issueList.route)
  const parentRow = page.locator(`[data-issue-row-id="${issueList.issues[0].id}"]`)
  await expect(parentRow).toBeVisible()
  const gap = await parentRow.evaluate(row => {
    const title = row.querySelector('[data-row-title]')!.getBoundingClientRect()
    const status = row.querySelector('[class*="statusTrigger"] svg')!.getBoundingClientRect()
    return title.x - status.right
  })
  expect(gap).toBe(9)
  const collapse = page.getByRole('button', { name: 'Collapse group', exact: true }).first()
  await expect(collapse).toHaveCSS('opacity', '1')
  await collapse.click()
  await expect(parentRow).not.toBeVisible()
  await page.getByRole('button', { name: 'Expand group', exact: true }).first().click()
  await page.getByRole('button', { name: 'Display options', exact: true }).click()
  const cycleProperty = page.getByRole('button', { name: 'Cycle', exact: true })
  if (await cycleProperty.getAttribute('aria-pressed') !== 'true') await cycleProperty.click()
  await page.getByRole('checkbox', { name: 'Nested sub-issues', exact: true }).check()
  await page.keyboard.press('Escape')
  const childRow = page.locator(`[data-issue-row-id="${child.id}"]`)
  await expect(childRow).toHaveAttribute('data-nested', 'true')
  await expect(childRow.locator('[class*="nestingBranch"]')).toHaveCount(1)
  const parentTitle = await parentRow.locator('[data-row-title]').boundingBox()
  const childTitle = await childRow.locator('[data-row-title]').boundingBox()
  expect(childTitle!.x - parentTitle!.x).toBe(24)
  await expect(parentRow.getByRole('button', { name: '0 of 1 sub-issues completed', exact: true })).toBeVisible()
  await expect(parentRow.locator('[class*="cycleChip"]')).toHaveCSS('border-radius', '48px')
  await expect(parentRow.locator('[class*="cycleChip"]')).toHaveCSS('height', '24px')
  if (process.env.FLOW_LIST_SHOTS) {
    await page.waitForTimeout(300)
    await page.mouse.move(0, 0)
    await page.screenshot({ path: `${process.env.FLOW_LIST_SHOTS}/desktop-cycle-nesting.png`, animations: 'disabled' })
  }
})

test('creates a filtered view from Save and persists a Teams breakdown filter', async ({ page, request, workspaceKey, issueList }) => {
  await page.goto(issueList.route)
  await page.getByRole('button', { name: 'Add filter', exact: true }).click()
  await page.getByRole('option', { name: 'Status', exact: true }).click()
  const state = issueList.data.states.find(state => state.type === 'unstarted')!
  await page.getByRole('option', { name: new RegExp(`^${state.name}`) }).click()
  await page.keyboard.press('Escape')
  const bar = page.getByLabel('Applied filters', { exact: true })
  await expect(bar).toHaveCSS('border-radius', '8px')
  await expect(bar.locator('[data-filter-chip]')).toHaveCSS('height', '24px')
  const save = bar.getByRole('button', { name: 'Create new view', exact: true })
  await expect(save).toHaveText('Save')
  await save.click()
  await page.getByRole('textbox', { name: 'View name', exact: true }).fill('[audit] Desktop queue')
  await page.getByRole('button', { name: /^Save to / }).click()
  await page.getByRole('menuitemradio', { name: 'Personal', exact: true }).click()
  const created = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/views')
  await page.getByRole('button', { name: 'Create view', exact: true }).click()
  const response = await created
  expect(response.ok()).toBeTruthy()
  const view = await response.json() as SavedView
  await expect(page).toHaveURL(new RegExp(`/view/${view.slugId || view.id}$`))
  expect(view.filters.some(filter => filter.field === 'status')).toBeTruthy()
  const panel = page.getByRole('complementary', { name: 'View sidebar' })
  await panel.getByRole('tab', { name: 'Teams', exact: true }).click()
  await panel.getByRole('tabpanel', { name: 'Teams', exact: true }).getByRole('button').first().click()
  await expect(page.getByRole('button', { name: 'Team values', exact: true })).toBeVisible()
  await page.locator('[data-saved-view-band-commands]').getByRole('button', { name: 'Save view options', exact: true }).click()
  const patched = page.waitForResponse(response => response.request().method() === 'PATCH' && new URL(response.url()).pathname === `/api/views/${view.id}`)
  await page.getByRole('menuitem', { name: /Save to this view/ }).click()
  const update = await patched
  expect(update.ok()).toBeTruthy()
  expect(((await update.json()) as SavedView).filters.some(filter => filter.field === 'team')).toBeTruthy()
  await page.reload()
  await expect(panel).toBeVisible()
  await expect(page.locator('[data-saved-view-band-commands]')).toHaveCount(0)
  const bootstrap = await request.get('/api/bootstrap', { headers: { 'X-Workspace-Key': workspaceKey } })
  expect(bootstrap.ok()).toBeTruthy()
  expect(((await bootstrap.json()) as BootstrapData).savedViews.find(item => item.id === view.id)?.filters.some(filter => filter.field === 'team')).toBeTruthy()
})

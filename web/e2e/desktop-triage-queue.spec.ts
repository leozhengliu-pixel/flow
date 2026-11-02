import { test, expect } from './issue-list-fixture'

test.skip(({ isMobile }) => isMobile, 'Desktop triage queue')
test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' })

test('enables a throwaway team queue, shows its count, and opens a real issue in the detail pane', async ({ page, request, workspaceKey, issueList }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const team = issueList.data.teams[0]
  const settings = await request.patch(`/api/teams/${team.id}/settings`, { headers, data: { triageEnabled: true } })
  expect(settings.ok()).toBeTruthy()
  const backlog = issueList.data.states.find(state => state.type === 'backlog')!
  for (let index = 0; index < 3; index++) {
    const response = await request.post('/api/issues', { headers, data: { title: `[audit] Triage check ${index + 1}`, teamId: team.id, stateId: backlog.id } })
    expect(response.status()).toBe(201)
  }
  const route = `/${workspaceKey}/team/${team.key}/triage`
  await page.goto(route)
  const nav = page.locator(`a[href="${route}"]`)
  await expect(nav).toHaveText('Triage3')
  const rows = page.locator('[data-triage-row]')
  await expect(rows).toHaveCount(3)
  await expect(page.locator('.flow-triage-list__team svg')).toBeVisible()
  await expect(rows.first()).toHaveCSS('padding', '0px')
  await expect(rows.first()).toHaveCSS('margin', '0px')
  const list = (await page.locator('[data-split-view-list]').boundingBox())!
  const row = (await rows.first().boundingBox())!
  expect(row.x).toBe(list.x)
  expect(list.width - row.width).toBeLessThanOrEqual(1)
  await expect(page.locator('.flow-triage-list__actions')).toHaveCSS('gap', '4px')
  const create = page.getByRole('button', { name: 'Create triage issue', exact: true })
  await expect(create).toHaveCSS('padding', '0px 8px 0px 6px')
  await expect(create.locator('svg')).toHaveCSS('margin-right', '6px')
  await expect(create).toHaveCSS('background-color', 'lch(10.149 0.593 272)')
  await expect(create).toHaveCSS('color', 'lch(90.451 1.2 272)')
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/triage-empty-selection.png`, animations: 'disabled' })
  const selectedTitle = '[audit] Triage check 2'
  const selectedRow = rows.filter({ hasText: selectedTitle })
  await selectedRow.click()
  const detail = page.locator('[data-split-view-detail]')
  const title = detail.locator('.title-editor')
  await expect(title).toHaveText(selectedTitle)
  await expect(title).toBeInViewport()
  const listBox = (await page.locator('[data-split-view-list]').boundingBox())!
  const paneBox = (await detail.boundingBox())!
  const titleBox = (await title.boundingBox())!
  expect(listBox.x + listBox.width).toBeLessThanOrEqual(paneBox.x)
  expect(titleBox.x).toBeGreaterThanOrEqual(paneBox.x)
  expect(titleBox.x + titleBox.width).toBeLessThanOrEqual(paneBox.x + paneBox.width)
  await expect(detail.locator('.issue-mobile-properties')).toBeVisible()
  const properties = detail.locator('.issue-mobile-properties')
  await expect(properties.locator('.labels-picker')).toHaveCSS('padding', '0px')
  const projectBox = (await properties.locator('.issue-project-trigger').boundingBox())!
  const labelsBox = (await properties.getByRole('button', { name: 'Change labels', exact: true }).boundingBox())!
  expect(labelsBox.x - projectBox.x - projectBox.width).toBeCloseTo(6, 1)
  await expect(detail.locator('.issue-document')).toHaveCSS('padding', '0px 0px 0px 14px')
  expect((await detail.locator('.issue-document').boundingBox())!.y - (await detail.locator('.issue-layout').boundingBox())!.y).toBe(21.75)
  const priority = detail.getByRole('combobox', { name: /Change priority/ })
  await expect(priority).toHaveCSS('padding', '0px 8px 0px 6px')
  await expect(priority).toHaveCSS('border-top-color', 'rgba(0, 0, 0, 0)')
  await expect(priority.locator(':scope > span').last()).toHaveCSS('font-weight', '450')
  await expect(detail.getByRole('button', { name: 'Accept issue from triage', exact: true })).toHaveCSS('padding', '0px 2px')
  await expect(detail.getByRole('region', { name: 'Similar issues', exact: true })).toHaveCount(0)
  await expect(detail.getByRole('button', { name: /Accept/ }).first()).toBeVisible()
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/triage-queue.png`, animations: 'disabled' })
  await detail.getByRole('button', { name: 'Accept issue from triage', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Accept', exact: true }).click()
  await expect(rows).toHaveCount(2)
  await expect(selectedRow).toHaveCount(0)
  await expect(nav).toHaveText('Triage2')
  await page.goto(issueList.route)
  await expect(nav).toHaveText('Triage2')
  const off = await request.patch(`/api/teams/${team.id}/settings`, { headers, data: { triageEnabled: false } })
  expect(off.ok()).toBeTruthy()
  await page.goto(route)
  await expect(page.getByText('Triage is turned off', { exact: true })).toBeVisible()
})

import { test, expect } from './issue-list-fixture'

test.skip(({ isMobile }) => isMobile, 'Desktop triage queue')
test.use({ viewport: { width: 1440, height: 900 } })

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

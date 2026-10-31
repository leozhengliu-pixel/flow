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
  expect((await page.locator('[data-split-view-list]').boundingBox())?.width).toBe(400)
  expect(Math.abs((await page.locator('[data-split-view-detail]').boundingBox())!.width - 787)).toBeLessThanOrEqual(1)
  expect((await rows.first().boundingBox())?.height).toBe(59)
  expect(Math.abs((await rows.first().boundingBox())!.width - 383.5)).toBeLessThanOrEqual(1)
  await expect(page.locator('.flow-triage-list__team svg')).toBeVisible()
  await rows.first().click()
  const detail = page.locator('[data-split-view-detail]')
  await expect(detail.locator('.issue-title-field')).toBeVisible()
  await expect(detail.getByRole('button', { name: /Accept/ }).first()).toBeVisible()
  await detail.getByRole('button', { name: 'Accept issue from triage', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Accept', exact: true }).click()
  await expect(rows).toHaveCount(2)
  await expect(nav).toHaveText('Triage2')
  await page.goto(issueList.route)
  await expect(nav).toHaveText('Triage2')
  const off = await request.patch(`/api/teams/${team.id}/settings`, { headers, data: { triageEnabled: false } })
  expect(off.ok()).toBeTruthy()
  await page.goto(route)
  await expect(page.getByText('Triage is turned off', { exact: true })).toBeVisible()
})

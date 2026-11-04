import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData, Issue, Team } from '../src/types/flow'

test.use({ viewport: { width: 1440, height: 900 } })
const shots = process.env.FLOW_TEAM_MOVE_SHOTS

test('moves from the Team property and shortcut, updates identifiers and redirects old URLs', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const source = data.teams[0]
  const createdTeam = await request.post(`/api/workspaces/${workspaceKey}/teams`, { headers, data: { name: 'Destination', key: 'DST' } })
  expect(createdTeam.status()).toBe(201)
  const target = await createdTeam.json() as Team
  const labelResponse = await request.post(`/api/teams/${source.id}/labels`, { headers, data: { name: 'Source only' } })
  expect(labelResponse.status()).toBe(201)
  const label = await labelResponse.json()
  const issueResponse = await request.post('/api/issues', { headers, data: { title: 'Move between teams', teamId: source.id, labelIds: [label.id] } })
  expect(issueResponse.status()).toBe(201)
  const issue = await issueResponse.json() as Issue
  const oldURL = `/${workspaceKey}/issue/${issue.identifier}`
  await page.goto(oldURL)
  const rail = page.locator('.issue-properties')
  await rail.getByRole('combobox', { name: 'Change team', exact: true }).click()
  const picker = page.getByRole('dialog', { name: 'Change Team', exact: true })
  await expect(picker.getByRole('heading', { name: 'Showing all items' })).toBeAttached()
  await picker.getByRole('option', { name: `${target.name} ${target.key}`, exact: true }).click()
  await expect(page.getByText(`Moved to ${target.name}`, { exact: true })).toBeVisible()
  const moved = await (await request.get(`/api/issue-records/${issue.id}`, { headers })).json() as Issue
  expect(moved.identifier).toMatch(/^DST-\d+$/)
  expect(moved.identifier).not.toBe(issue.identifier)
  expect(moved.labels.some(item => item.id === label.id)).toBe(false)
  await expect(page.locator('.issue-breadcrumb-current')).toContainText(moved.identifier)
  if (shots) await page.screenshot({ path: `${shots}/detail-moved.png`, animations: 'disabled' })
  await page.goto(oldURL)
  await expect(page).toHaveURL(new RegExp(`/issue/${moved.identifier}(?:/|$)`))
  await expect(page.locator('.issue-breadcrumb-current')).toContainText(moved.identifier)
  await rail.getByRole('combobox', { name: 'Change team', exact: true }).focus()
  const modifier = await page.evaluate(() => /mac|iphone|ipad|ipod/i.test((navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform ?? navigator.platform)) ? 'Meta' : 'Control'
  await page.keyboard.press(`${modifier}+Shift+M`)
  await expect(picker).toBeVisible()
  await page.keyboard.type(source.key)
  await expect(picker.getByRole('option')).toHaveCount(1)
  await page.keyboard.press('Enter')
  await expect(page.getByText(`Moved to ${source.name}`, { exact: true })).toBeVisible()
  const back = await (await request.get(`/api/issue-records/${issue.id}`, { headers })).json() as Issue
  expect(back.team.id).toBe(source.id)
  expect(back.identifier).not.toBe(issue.identifier)
  await expect(page.locator('.issue-breadcrumb-current')).toContainText(back.identifier)
  await page.goto(`/${workspaceKey}/issue/${moved.identifier}`)
  await expect(page).toHaveURL(new RegExp(`/issue/${back.identifier}(?:/|$)`))
  await page.goto(`/${workspaceKey}/my-issues/created`)
  const row = page.locator(`[data-issue-row-id="${issue.id}"]`)
  await expect(row).toBeVisible()
  await row.focus()
  await page.keyboard.press(`${modifier}+Shift+M`)
  const teamSearch = page.locator('.issue-row-team-choices input')
  await expect(teamSearch).toBeFocused()
  if (shots) await page.screenshot({ path: `${shots}/row-team-shortcut.png`, animations: 'disabled' })
  await page.keyboard.type(target.key)
  await page.keyboard.press('Enter')
  await expect(page.getByText(`Moved to ${target.name}`, { exact: true })).toBeVisible()
  const fromRow = await (await request.get(`/api/issue-records/${issue.id}`, { headers })).json() as Issue
  expect(fromRow.team.id).toBe(target.id)
  await page.goto(`/${workspaceKey}/issue/${fromRow.identifier}`)
  await expect(page.locator('.issue-breadcrumb-current')).toContainText(fromRow.identifier)
  await page.keyboard.press(`${modifier}+k`)
  await page.getByRole('option', { name: /Move to.*team/ }).click()
  await expect(page.getByRole('group', { name: 'Showing all items' })).toBeVisible()
  if (shots) await page.screenshot({ path: `${shots}/command-team-picker.png`, animations: 'disabled' })
  await page.getByRole('option', { name: `${source.name} ${source.key}`, exact: true }).click()
  await expect(page.getByText(`Moved to ${source.name}`, { exact: true })).toBeVisible()
})

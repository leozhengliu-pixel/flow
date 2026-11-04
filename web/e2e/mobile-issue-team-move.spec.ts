import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData, Issue, Team } from '../src/types/flow'

const shots = process.env.FLOW_TEAM_MOVE_SHOTS

for (const width of [390, 375]) {
  test(`changes team from phone issue options without a property chip at ${width}px`, async ({ page, request, workspaceKey }) => {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 667 })
    const headers = { 'X-Workspace-Key': workspaceKey }
    const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
    const response = await request.post(`/api/workspaces/${workspaceKey}/teams`, { headers, data: { name: 'Destination', key: 'DST' } })
    expect(response.status()).toBe(201)
    const target = await response.json() as Team
    const created = await request.post('/api/issues', { headers, data: { title: 'Move from phone options', teamId: data.teams[0].id } })
    expect(created.status()).toBe(201)
    const issue = await created.json() as Issue
    const oldURL = `/${workspaceKey}/issue/${issue.identifier}`
    await page.goto(oldURL)
    await expect(page.locator('.issue-mobile-properties')).toBeVisible()
    await expect(page.locator('.issue-mobile-properties [data-property=team]')).toHaveCount(0)
    if (shots) await page.screenshot({ path: `${shots}/phone-${width}-detail.png`, animations: 'disabled' })
    await page.getByRole('button', { name: 'Issue options', exact: true }).click()
    const options = page.getByRole('dialog', { name: 'Issue options', exact: true })
    await expect(options.getByRole('option').first()).toHaveText('Team')
    if (shots) await options.screenshot({ path: `${shots}/phone-${width}-options.png`, animations: 'disabled' })
    await options.getByRole('option', { name: 'Team', exact: true }).click()
    const teams = page.getByRole('dialog', { name: 'Team', exact: true })
    await expect(teams.getByRole('heading', { name: 'Showing all items' })).toBeAttached()
    await expect(teams.locator('[data-current=true]')).toContainText(issue.team.name)
    if (shots) await teams.screenshot({ path: `${shots}/phone-${width}-team.png`, animations: 'disabled' })
    await teams.getByRole('option', { name: target.name, exact: true }).click()
    await expect(page.getByText(`Moved to ${target.name}`, { exact: true })).toBeVisible()
    const moved = await (await request.get(`/api/issue-records/${issue.id}`, { headers })).json() as Issue
    expect(moved.team.id).toBe(target.id)
    expect(moved.identifier).toMatch(/^DST-\d+$/)
    await expect(page.locator('.issue-breadcrumb-current')).toContainText(moved.identifier)
    await page.goto(oldURL)
    await expect(page).toHaveURL(new RegExp(`/issue/${moved.identifier}(?:/|$)`))
    await expect(page.locator('.issue-mobile-properties [data-property=team]')).toHaveCount(0)
  })
}

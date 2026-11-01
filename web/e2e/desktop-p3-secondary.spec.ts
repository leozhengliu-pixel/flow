import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData, Issue } from '../src/types/flow'

test.skip(({ isMobile }) => isMobile, 'Desktop secondary chrome')
test.use({ viewport: { width: 1440, height: 900 } })

test('uses title padding instead of a fitted inbox offset and keeps the team icon gap', async ({ page, request, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/inbox`)
  const title = page.locator('.flow-inbox__header h2')
  await expect(title).toHaveCSS('padding', '0px 4px 0px 10px')
  await expect(page.locator('.flow-inbox__header-title-actions')).toHaveCSS('margin-left', '0px')
  const response = await request.get('/api/bootstrap', { headers: { 'X-Workspace-Key': workspaceKey } })
  const data = await response.json() as BootstrapData
  await page.goto(`/${workspaceKey}/team/${data.teams[0].key}/documents`)
  await expect(page.locator('.team-home-topbar')).toHaveCSS('gap', '6px')
  const headers = { 'X-Workspace-Key': workspaceKey }
  const issues: Issue[] = []
  for (const title of ['Review target', 'Related target']) {
    const created = await request.post('/api/issues', { headers, data: { title, teamId: data.teams[0].id } })
    expect(created.status()).toBe(201)
    issues.push(await created.json() as Issue)
  }
  const relation = await request.post(`/api/issues/${issues[0].id}/relations`, { headers, data: { type: 'related', relatedIssueId: issues[1].id } })
  expect(relation.status()).toBe(201)
  await page.goto(`/${workspaceKey}/issue/${issues[0].identifier}`)
  await page.getByRole('region', { name: 'Related', exact: true }).getByRole('link', { name: 'Related target', exact: true }).hover()
  const preview = page.locator('.issue-relation-card__body')
  await expect(preview).toBeVisible()
  await expect(preview.locator('.issue-relation-card__identifier')).toHaveCSS('font-size', '12px')
  await expect(preview.getByText('No priority', { exact: true })).toHaveCount(0)
})

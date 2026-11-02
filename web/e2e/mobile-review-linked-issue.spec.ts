import { expect } from '@playwright/test'
import { test, bootstrap, createReview, capture } from './review-findings-fixture'

test.use({ viewport: { width: 390, height: 844 } })
test('opens the linked-issue command sheet and persists unlinking', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await bootstrap(request, workspaceKey)
  const issueResponse = await request.post('/api/issues', { headers, data: { title: 'Linked review issue', teamId: data.teams[0].id, stateId: 'state_todo' } })
  expect(issueResponse.status()).toBe(201)
  const issue = await issueResponse.json() as { id: string }
  const id = await createReview(request, workspaceKey)
  expect((await request.patch(`/api/reviews/${id}`, { headers, data: { issueIds: [issue.id] } })).ok()).toBe(true)
  await page.goto(`/${workspaceKey}/review/github-1`)
  await page.getByRole('button', { name: 'Issue actions', exact: true }).click()
  const menu = page.getByRole('dialog', { name: 'Issue actions' })
  await expect(menu).toBeVisible()
  await capture(menu, 'flow-review-issue-menu')
  await menu.getByRole('option', { name: 'Unlink issue from pull request' }).click()
  await expect(page.locator('.review-mobile-issue')).toHaveCount(0)
  const updated = await request.get(`/api/reviews/${id}`, { headers })
  expect((await updated.json()).issueIds).toEqual([])
})

import { expect } from '@playwright/test'
import { test, bootstrap, capture } from './review-findings-fixture'

test.use({ viewport: { width: 390, height: 844 } })
test('copies the selected issue route from the inbox copy strip', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await bootstrap(request, workspaceKey)
  const response = await request.post('/api/issues', { headers, data: { title: 'Inbox link destination', teamId: data.teams[0].id, stateId: 'state_todo' } })
  expect(response.status()).toBe(201)
  const issue = await response.json() as { id: string; identifier: string }
  expect((await request.post(`/api/issues/${issue.id}/reminders`, { headers, data: { remindAt: new Date(Date.now() + 300).toISOString() } })).ok()).toBe(true)
  await expect.poll(async () => (await (await request.get('/api/notifications?limit=50', { headers })).json()).notifications.length).toBeGreaterThan(0)
  await page.addInitScript(() => Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async (text: string) => { document.documentElement.dataset.copiedIssueLink = text } } }))
  await page.goto(`/${workspaceKey}/inbox`)
  await page.locator('.flow-inbox-notification-list [role=link]').first().click()
  await page.getByRole('button', { name: 'Copy issue URL', exact: true }).click()
  const copied = await page.locator('html').getAttribute('data-copied-issue-link')
  expect(copied).toContain(`/issue/${issue.identifier}/`)
  expect(new URL(copied!).pathname).not.toBe(`/${workspaceKey}/inbox`)
  await capture(page.locator('.flow-inbox-detail'), 'flow-inbox-copy-link')
})

import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData, Issue } from '../src/types/flow'

test('suggests a comment action, accepts once with activity, and dismisses another', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  expect((await request.patch('/api/account/settings', { headers, data: { agentEnabled: true, suggestedActionsOnComments: true } })).status()).toBe(200)
  const created = await request.post('/api/issues', { headers, data: { title: 'Comment action verification', teamId: data.teams[0].id, priority: 0 } })
  expect(created.status()).toBe(201)
  const issue = await created.json() as Issue
  await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
  const composer = page.locator('.composer').filter({ has: page.getByRole('button', { name: 'Submit comment' }) }).last()
  await composer.getByRole('textbox').fill('Please make this urgent.')
  await composer.getByRole('button', { name: 'Submit comment' }).click()
  const suggestion = page.getByLabel('Suggested actions').filter({ hasText: 'Set priority to Urgent' })
  await expect(suggestion).toBeVisible()
  const other = await page.context().newPage()
  await other.goto(`/${workspaceKey}/issue/${issue.identifier}`)
  await expect(other.getByLabel('Suggested actions')).toContainText('Set priority to Urgent')
  await suggestion.getByRole('button', { name: 'Accept', exact: true }).click()
  await expect(suggestion.getByRole('status')).toHaveText('Accepted')
  await expect(other.getByLabel('Suggested actions').getByRole('status')).toHaveText('Accepted')
  const read = () => request.get(`/api/issue-records/${issue.id}`, { headers })
  await expect.poll(async () => (await (await read()).json() as Issue).priority).toBe(1)
  // The creator accepts within three minutes of creating the issue, so the change folds into the creation (the reference's
  // creation grace window) and leaves no separate priority row; the issue itself carries the accepted priority.
  const history = () => request.get(`/api/issue-records/${issue.id}/history`, { headers })
  const priorityRows = (await (await history()).json()).activities.filter((event: { type: string; metadata: Record<string, string> }) => event.type === 'issue.updated' && event.metadata.priority === 'Urgent')
  expect(priorityRows).toHaveLength(0)
  await composer.getByRole('textbox').fill('Please make this high priority instead.')
  await composer.getByRole('button', { name: 'Submit comment' }).click()
  const second = page.getByLabel('Suggested actions').filter({ hasText: 'Set priority to High' })
  await expect(second).toBeVisible()
  await expect(other.getByLabel('Suggested actions').filter({ hasText: 'Set priority to High' })).toBeVisible()
  await second.getByRole('button', { name: 'Dismiss suggestion' }).click()
  await expect(second).toHaveCount(0)
  await expect(other.getByLabel('Suggested actions').filter({ hasText: 'Set priority to High' })).toHaveCount(0)
  expect((await (await read()).json() as Issue).priority).toBe(1)
  await other.close()
  await page.goto(`/${workspaceKey}/settings/account/agents`)
  const toggle = page.getByRole('checkbox', { name: 'Enable suggested actions on comments' })
  await expect(toggle).toBeChecked()
  await page.getByText('Enable suggested actions on comments', { exact: true }).click()
  await page.reload()
  await expect(toggle).not.toBeChecked()
})

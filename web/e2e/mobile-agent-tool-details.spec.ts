import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.use({ viewport: { width: 375, height: 667 }, deviceScaleFactor: 3, launchOptions: { args: ['--force-device-scale-factor=3'] } })

test('expanded work retains the target of each phone tool call', async ({ page, workspaceKey }) => {
  const now = new Date().toISOString()
  const session = { id: 'tool-details', slugId: 'tool-details', title: 'Tool details', userId: 'usr_local', location: 'page', favorite: false, issueIds: [], skillIds: [], createdAt: now, updatedAt: now, messages: [
    { id: 'prompt', role: 'user', content: 'Read the two issues', createdAt: now },
    { id: 'answer', role: 'assistant', content: 'Done.', createdAt: now, durationMs: 1000, parts: ['TST-1', 'TST-2'].map((identifier, index) => ({ id: `tool-${index}`, type: 'toolCall', status: 'completed', toolCall: { id: `call-${index}`, name: 'get_issue', title: 'Read issue', arguments: { id: identifier }, result: { identifier }, status: 'completed' } })) },
  ] }
  await page.route('**/api/agent/sessions/tool-details', route => route.fulfill({ json: session }))
  await page.route('**/api/bootstrap*', async route => {
    const response = await route.fetch(), data = await response.json()
    await route.fulfill({ response, json: { ...data, agentSessions: [session] } })
  })
  await page.goto(`/${workspaceKey}/agent/tool-details`)
  await page.locator('details summary').first().click()
  for (const identifier of ['TST-1', 'TST-2']) {
    const target = page.locator('[class*=toolCallRow] > span').filter({ hasText: identifier })
    await expect(target).toBeVisible()
    expect((await target.boundingBox())!.width).toBeGreaterThan(0)
  }
})

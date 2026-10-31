import { expect, type APIRequestContext, type Page } from '@playwright/test'
import { test } from './workspace-fixture'

type Issue = { id: string; identifier: string }
type Notification = { id: string; issueId?: string; recipientId?: string }

async function workspaceTeam(request: APIRequestContext, key: string) {
  const response = await request.get('/api/bootstrap', { headers: { 'X-Workspace-Key': key } })
  expect(response.status()).toBe(200)
  const workspace = await response.json() as { teams?: Array<{ id: string }> }
  const teamId = workspace.teams?.[0]?.id
  expect(teamId).toBeTruthy()
  return teamId!
}

async function createIssue(request: APIRequestContext, headers: Record<string, string>, teamId: string, title: string, stateId: string): Promise<Issue> {
  const response = await request.post(`/api/issues`, {
    headers,
    data: { title, description: 'Inbox stale cache', teamId, stateId },
  })
  expect(response.status()).toBe(201)
  return response.json() as Promise<Issue>
}

async function remind(request: APIRequestContext, headers: Record<string, string>, issueId: string) {
  const response = await request.post(`/api/issues/${issueId}/reminders`, {
    headers,
    data: { remindAt: new Date(Date.now() + 300).toISOString() },
  })
  expect(response.status()).toBe(201)
}

async function notifications(request: APIRequestContext, headers: Record<string, string>) {
  const response = await request.get(`/api/notifications?limit=50`, { headers })
  expect(response.status()).toBe(200)
  return response.json() as Promise<{ notifications: Notification[]; unreadCount: number }>
}

async function seedInbox(request: APIRequestContext, headers: Record<string, string>, teamId: string) {
  const first = await createIssue(request, headers, teamId, 'Stale inbox first', 'state_todo')
  const second = await createIssue(request, headers, teamId, 'Stale inbox second', 'state_todo')
  await remind(request, headers, first.id)
  await remind(request, headers, second.id)
  await expect.poll(async () => (await notifications(request, headers)).notifications.length, { timeout: 10_000 }).toBe(2)
  return [first, second]
}

async function renameNewInbox(request: APIRequestContext, headers: Record<string, string>, teamId: string, old: Notification[]) {
  for (const item of old) {
    const response = await request.patch(`/api/notifications/${item.id}`, { headers, data: { deleted: true } })
    expect(response.status()).toBe(200)
  }
  const first = await createIssue(request, headers, teamId, 'Fresh inbox first', 'state_progress')
  const second = await createIssue(request, headers, teamId, 'Fresh inbox second', 'state_progress')
  await remind(request, headers, first.id)
  await remind(request, headers, second.id)
  await expect.poll(async () => (await notifications(request, headers)).notifications.length, { timeout: 10_000 }).toBe(2)
  return { first, second }
}

async function expectFreshInbox(page: Page, request: APIRequestContext, headers: Record<string, string>, issues: Issue[], cacheKey: string, old: Notification[]) {
  const identifierByIssue = new Map(issues.map(issue => [issue.id, issue.identifier]))
  const expected = (await notifications(request, headers)).notifications.map(item => identifierByIssue.get(item.issueId ?? ''))
  const rows = page.locator('.flow-inbox-notification-list > [role="listitem"]')
  const identifiers = page.locator('.flow-inbox-row__identifier')
  await expect(rows).toHaveCount(2)
  await expect.poll(async () => identifiers.allTextContents()).toEqual(expected)
  await expect(page).toHaveTitle(/Inbox \(2\)/)

  // Rendering the fresh inbox precedes its idle cache write.
  await expect.poll(async () => page.evaluate(key => Boolean(localStorage.getItem(key)), cacheKey)).toBe(true)
  const cached = await page.evaluate(key => {
    const raw = localStorage.getItem(key)
    return raw ? JSON.parse(raw) as { data: { notifications: unknown[] } } : null
  }, cacheKey)
  expect(cached?.data.notifications).toEqual([])
  for (const item of old) expect(JSON.stringify(cached)).not.toContain(item.id)
}

test.describe('inbox stale navigation cache', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })

  test('reloads the server rows after the cached notifications are deleted', async ({ page, request, workspaceKey }, testInfo) => {
    const teamId = await workspaceTeam(request, workspaceKey)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const oldIssues = await seedInbox(request, headers, teamId)

    const boot = await (await request.get(`/api/bootstrap?workspace=${workspaceKey}`, { headers })).json() as { viewer: { id: string } }
    const cacheKey = `flow:navigation:${boot.viewer.id}:${workspaceKey}`
    await page.goto(`/${workspaceKey}/inbox`)
    await expect(page.locator('.flow-inbox-notification-list > [role="listitem"]')).toHaveCount(2)
    await expect.poll(async () => page.evaluate(key => Boolean(localStorage.getItem(key)), cacheKey)).toBe(true)
    const old = (await notifications(request, headers)).notifications
    expect(old).toHaveLength(2)

    const fresh = await renameNewInbox(request, headers, teamId, old)
    await page.reload()

    await expectFreshInbox(page, request, headers, [fresh.first, fresh.second], cacheKey, old)
    if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/inbox-after-delete-reload-${testInfo.project.name}.png`, fullPage: true, animations: 'disabled' })
    expect(oldIssues).toHaveLength(2)
  })

  test('recovers when the bootstrap stream is cut off before notifications', async ({ page, request, workspaceKey }, testInfo) => {
    const teamId = await workspaceTeam(request, workspaceKey)
    const headers = { 'X-Workspace-Key': workspaceKey }
    await seedInbox(request, headers, teamId)

    const boot = await (await request.get(`/api/bootstrap?workspace=${workspaceKey}`, { headers })).json() as { viewer: { id: string } }
    const cacheKey = `flow:navigation:${boot.viewer.id}:${workspaceKey}`
    await page.goto(`/${workspaceKey}/inbox`)
    await expect(page.locator('.flow-inbox-notification-list > [role="listitem"]')).toHaveCount(2)
    await expect.poll(async () => page.evaluate(key => Boolean(localStorage.getItem(key)), cacheKey)).toBe(true)
    const old = (await notifications(request, headers)).notifications
    const fresh = await renameNewInbox(request, headers, teamId, old)

    // Cut the stream where the legacy bootstrap starts notifications: the
    // browser receives a partial body, just as a cancelled stream would.
    const bootstrap = await (await request.get(`/api/bootstrap?workspace=${workspaceKey}`, { headers })).text()
    const cut = bootstrap.indexOf('"notifications"')
    expect(cut).toBeGreaterThan(0)
    let served = 0
    await page.route('**/api/bootstrap*', async route => {
      if (served >= 2) return route.continue()
      served += 1
      await route.fulfill({ status: 200, contentType: 'application/json; charset=utf-8', body: bootstrap.slice(0, cut) })
    })
    await page.reload()

    await expectFreshInbox(page, request, headers, [fresh.first, fresh.second], cacheKey, old)
    if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/inbox-after-cut-stream-${testInfo.project.name}.png`, fullPage: true, animations: 'disabled' })
  })
})

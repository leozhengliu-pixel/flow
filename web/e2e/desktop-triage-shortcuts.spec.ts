import { test as base, expect } from './issue-list-fixture'
import type { Issue } from '../src/types/flow'

const test = base.extend<{ queue: { route: string } }>({
  queue: async ({ request, workspaceKey, issueList }, provide) => {
    const headers = { 'X-Workspace-Key': workspaceKey }
    const team = issueList.data.teams[0]
    expect((await request.patch(`/api/teams/${team.id}/settings`, { headers, data: { triageEnabled: true } })).ok()).toBeTruthy()
    for (let index = 0; index < 3; index++) {
      const response = await request.post('/api/issues', { headers, data: { title: `[audit] Shortcut queue ${index + 1}`, teamId: team.id, stateId: issueList.data.states.find(state => state.type === 'backlog')!.id } })
      expect(response.status()).toBe(201)
    }
    await provide({ route: `/${workspaceKey}/team/${team.key}/triage` })
  },
})

test.skip(({ isMobile }) => isMobile, 'Desktop triage shortcuts')
test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' })

test('J/K and arrows select next/previous without wrapping and ignore editors and dialogs', async ({ page, queue }) => {
  await page.goto(queue.route)
  const rows = page.locator('[data-triage-row]')
  await expect(rows).toHaveCount(3)
  await page.keyboard.press('j')
  await expect(rows.nth(0)).toHaveAttribute('aria-current', 'page')
  await page.keyboard.press('j')
  await expect(rows.nth(1)).toHaveAttribute('aria-current', 'page')
  await page.keyboard.press('k')
  await expect(rows.nth(0)).toHaveAttribute('aria-current', 'page')
  await page.keyboard.press('ArrowUp')
  await expect(rows.nth(0)).toHaveAttribute('aria-current', 'page')
  await page.keyboard.press('ArrowDown')
  await expect(rows.nth(1)).toHaveAttribute('aria-current', 'page')
  await page.keyboard.press('ArrowDown')
  await expect(rows.nth(2)).toHaveAttribute('aria-current', 'page')
  await page.keyboard.press('j')
  await expect(rows.nth(2)).toHaveAttribute('aria-current', 'page')
  const title = page.getByRole('textbox', { name: 'Issue title', exact: true })
  await title.click()
  await page.keyboard.press('1')
  await page.keyboard.press('j')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(rows.nth(2)).toHaveAttribute('aria-current', 'page')
  await page.keyboard.press('Escape')
  await page.locator('.issue-header-context').click()
  await page.keyboard.press('1')
  const accept = page.getByRole('dialog', { name: 'Accept issue' })
  await expect(accept).toBeVisible()
  await page.keyboard.press('j')
  await page.keyboard.press('2')
  await page.keyboard.press('h')
  await expect(accept).toBeVisible()
  await expect(page.getByRole('dialog')).toHaveCount(1)
})

for (const action of [{ key: '1', title: 'Accept issue', command: 'Accept', type: 'unstarted' }, { key: '2', title: 'Decline issue', command: 'Decline', type: 'canceled' }]) {
  test(`${action.key} opens ${action.command} and persists its status before advancing`, async ({ page, request, workspaceKey, queue }) => {
    await page.goto(queue.route)
    const rows = page.locator('[data-triage-row]')
    await rows.first().click()
    await expect(page.getByRole('button', { name: 'Accept issue from triage', exact: true })).toBeVisible()
    const id = await rows.first().getAttribute('data-triage-row')
    await page.keyboard.press(action.key)
    const dialog = page.getByRole('dialog', { name: action.title })
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: action.command, exact: true }).click()
    await expect(dialog).toBeHidden()
    await expect(rows).toHaveCount(2)
    const response = await request.get(`/api/issue-records/${id}`, { headers: { 'X-Workspace-Key': workspaceKey } })
    expect(response.ok()).toBeTruthy()
    const issue = await response.json() as Issue
    expect(issue.state.type).toBe(action.type)
    await expect(page.locator('[data-triage-row][aria-current=page]')).toHaveCount(1)
    await page.reload()
    await expect(rows).toHaveCount(2)
  })
}

for (const keys of [['3'], ['m', 'm']]) {
  test(`${keys.join(' ')} marks an issue as duplicate and records the relation`, async ({ page, request, workspaceKey, issueList, queue }) => {
    await page.goto(queue.route)
    const rows = page.locator('[data-triage-row]')
    await rows.first().click()
    await expect(page.getByRole('button', { name: 'Accept issue from triage', exact: true })).toBeVisible()
    const id = await rows.first().getAttribute('data-triage-row')
    for (const key of keys) await page.keyboard.press(key)
    const dialog = page.getByRole('dialog', { name: 'Mark issue as duplicate of' })
    await expect(dialog).toBeVisible()
    const target = issueList.issues[0]
    await dialog.getByRole('combobox').fill(target.identifier)
    await dialog.getByRole('option').filter({ hasText: target.title }).click()
    await expect(dialog).toBeHidden()
    await expect(rows).toHaveCount(2)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const response = await request.get(`/api/issue-records/${id}`, { headers })
    expect(response.ok()).toBeTruthy()
    const issue = await response.json() as Issue
    expect(['canceled', 'duplicate']).toContain(issue.state.type)
    expect(issue.relations).toEqual(expect.arrayContaining([expect.objectContaining({ type: 'duplicate', relatedIssueId: target.id })]))
    await page.reload()
    await expect(rows).toHaveCount(2)
  })
}

test('H snoozes with a parsed time, persists it, and advances to the next issue', async ({ page, request, workspaceKey, queue }) => {
  await page.goto(queue.route)
  const rows = page.locator('[data-triage-row]')
  await rows.first().click()
  await expect(page.getByRole('button', { name: 'Accept issue from triage', exact: true })).toBeVisible()
  const id = await rows.first().getAttribute('data-triage-row')
  await page.keyboard.press('h')
  const dialog = page.getByRole('dialog', { name: 'Snooze issue until' })
  await expect(dialog).toBeVisible()
  await dialog.getByRole('combobox').fill('in 2 days')
  await dialog.getByRole('option').filter({ hasText: 'Snooze until' }).click()
  await expect(dialog).toBeHidden()
  await expect(rows).toHaveCount(2)
  const response = await request.get(`/api/issue-records/${id}`, { headers: { 'X-Workspace-Key': workspaceKey } })
  expect(response.ok()).toBeTruthy()
  const issue = await response.json() as Issue
  expect(new Date(issue.snoozedUntil!).getTime()).toBeGreaterThan(Date.now() + 47 * 60 * 60 * 1000)
  await expect(page.locator('[data-triage-row][aria-current=page]')).toHaveCount(1)
  await page.reload()
  await expect(rows).toHaveCount(2)
})

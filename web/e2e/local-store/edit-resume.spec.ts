import { expect } from '@playwright/test'
import type { BootstrapData, Issue, Notification } from '../../src/types/flow'
import { editIssueTitle, readLocalRow, reloadDocument, signIn, test } from './fixture'

test.use({ workspaceNamePrefix: 'Edit resume' })
test.skip(process.env.FLOW_LOCAL_STORE_REAL_AUTH !== '1', 'Requires a subscriber and a different signed-in editor')

test('a member edit that notifies the owner keeps the issue baseline across reload', async ({ context, request, page, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const { viewer, teams: [team], states } = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const state = states.find(state => state.teamId === team.id && state.type === 'unstarted') ??
    states.find(state => !state.teamId && state.type === 'unstarted')
  expect(state).toBeDefined()
  const member = await signIn(context.request, 'member')
  const invitation = await request.post(`/api/workspaces/${workspaceKey}/invitations`, {
    data: { emails: [member.email], role: 'member', teamIds: [team.id] },
  })
  expect(invitation.status()).toBe(201)
  const [{ token }] = await invitation.json()
  expect((await context.request.post('/api/invitations/accept', { data: { token } })).status()).toBe(200)
  expect((await context.request.patch('/api/account/settings', {
    headers, data: { timezone: 'UTC', language: 'en-US', pulseSchedule: 'never' },
  })).ok()).toBe(true)
  await context.addInitScript(({ workspaceKey, viewerId }) => {
    sessionStorage.setItem(`flow.timezone-saved:${workspaceKey}:${viewerId}`, 'UTC')
  }, { workspaceKey, viewerId: member.id })

  const created = await request.post('/api/issue-records', {
    headers, data: { title: 'Owner subscribed to this issue', teamId: team.id, stateId: state!.id },
  })
  expect(created.status()).toBe(201)
  const issue = await created.json() as Issue
  expect(issue.subscriberIds).toContain(viewer.id)
  const path = `/${workspaceKey}/team/${team.key.toLowerCase()}/all`
  await page.goto(path)
  await expect(page.getByText(issue.title, { exact: true })).toBeVisible()
  await expect.poll(() => readLocalRow(page, issue.id)).toMatchObject({ title: issue.title })
  await reloadDocument(page)
  const resumes = () => page.evaluate(() => performance.getEntriesByName('flow:local-store:resume', 'mark').length)
  await expect.poll(resumes).toBe(1)
  const checkpoint = await readLocalRow(page, 'workspace', '_meta')
  expect(checkpoint).toHaveProperty('resume')
  const baseline = (checkpoint as unknown as { baseline: string }).baseline

  const edited = 'Member edited the title'
  const write = page.waitForResponse(response => response.url().endsWith(`/api/issue-records/${issue.id}`) && response.request().method() === 'PATCH')
  await page.getByText(issue.title, { exact: true }).click()
  await editIssueTitle(page.getByRole('textbox', { name: 'Issue title', exact: true }), edited)
  const response = await write
  expect(response.status()).toBe(200)
  const receipt = await response.request().headerValue('X-Flow-Local-Mutation')
  expect(receipt).toMatch(/^[a-f0-9-]{36}$/)
  expect(await response.json()).toMatchObject({ title: edited, version: issue.version + 1 })
  const inbox = await request.get('/api/notifications?limit=50', { headers })
  expect(inbox.ok()).toBe(true)
  const { notifications } = await inbox.json() as { notifications: Notification[] }
  expect(notifications).toContainEqual(expect.objectContaining({
    issueId: issue.id, recipientId: viewer.id, actor: expect.objectContaining({ id: member.id }),
  }))
  await expect.poll(() => readLocalRow(page, receipt!, '_outbox')).toMatchObject({ outcome: { ok: true } })
  await expect.poll(() => readLocalRow(page, issue.id)).toMatchObject({ title: edited, version: issue.version + 1 })
  expect(await readLocalRow(page, 'workspace', '_meta')).toMatchObject({ baseline, resume: {} })
  const eraseMarks = () => page.evaluate(() => performance.getEntriesByType('mark').map(mark => mark.name)
    .filter(name => name.startsWith('flow:local-store:erase:')))
  expect(await eraseMarks()).toEqual([])

  await page.goto(path)
  await expect(page.getByText(edited, { exact: true })).toBeVisible()
  await expect.poll(resumes).toBe(1)
  await reloadDocument(page)
  await expect(page.getByText(edited, { exact: true })).toBeVisible()
  await expect.poll(resumes).toBe(1)
  expect(await readLocalRow(page, 'workspace', '_meta')).toMatchObject({ baseline, resume: {} })
  expect(await readLocalRow(page, issue.id)).toMatchObject({ title: edited, version: issue.version + 1 })
  expect(await eraseMarks()).toEqual([])
})

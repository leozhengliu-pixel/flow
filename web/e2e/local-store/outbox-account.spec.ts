import { expect } from '@playwright/test'
import { editIssueTitle, interruptIssueWrites, readLocalRow, signIn, test } from './fixture'

test.use({ workspaceNamePrefix: 'Outbox-account' })

test('discards queued edits when a different account signs in to the same workspace', async ({ page, context, request, workspaceKey }) => {
  test.skip(process.env.FLOW_LOCAL_STORE_REAL_AUTH !== '1', 'Requires real browser sessions')
  const headers = { 'X-Workspace-Key': workspaceKey }
  const member = await signIn(context.request, 'member')
  const memberCookies = (await context.request.storageState()).cookies
  const ownerCookies = (await request.storageState()).cookies
  const { workspace, teams: [team], states } = await (await request.get('/api/bootstrap', { headers })).json() as {
    workspace: { id: string }; teams: { id: string; key: string }[]; states: { id: string; type: string; teamId?: string }[]
  }
  const invite = await request.post(`/api/workspaces/${workspaceKey}/invitations`, {
    data: { emails: [member.email], role: 'member', teamIds: [team.id] },
  })
  expect(invite.status()).toBe(201)
  const [{ token }] = await invite.json()
  expect((await context.request.post('/api/invitations/accept', { data: { token } })).ok()).toBe(true)
  for (const client of [request, context.request]) {
    expect((await client.patch('/api/account/settings', { headers, data: { language: 'en-US', pulseSchedule: 'never', timezone: 'UTC' } })).ok()).toBe(true)
  }
  await context.addCookies(ownerCookies)
  const { user: owner } = await (await context.request.get('/api/auth/session')).json()
  const state = states.find(state => state.teamId === team.id && state.type === 'unstarted') ?? states.find(state => !state.teamId && state.type === 'unstarted')
  expect(state).toBeDefined()
  const title = 'Shared issue before switching accounts'
  const response = await request.post('/api/issue-records', { headers, data: { title, teamId: team.id, stateId: state!.id } })
  expect(response.status()).toBe(201)
  const { id } = await response.json() as { id: string }
  const issueURL = `/api/issue-records/${id}`
  const list = `/${workspaceKey}/team/${team.key.toLowerCase()}/all`
  let mutationId = ''
  let attempts = 0
  await interruptIssueWrites(context, issueURL, (_, id) => {
    attempts++
    mutationId = id
    return 'disconnect'
  })
  await page.goto(list, { waitUntil: 'commit' })
  await expect.poll(() => readLocalRow(page, 'workspace', '_meta', workspace.id)).toMatchObject({ resume: { schema: 2 } })
  await page.getByText(title, { exact: true }).click()
  const editor = page.getByRole('textbox', { name: 'Issue title', exact: true })
  const pendingTitle = 'The previous account queued this edit'
  await expect(editor).toHaveText(title)
  await editIssueTitle(editor, pendingTitle)
  await expect.poll(() => mutationId).toMatch(/^[0-9a-f-]{36}$/)
  await expect.poll(() => readLocalRow(page, mutationId, '_outbox', workspace.id)).toMatchObject({ input: { title: pendingTitle } })
  await page.close()
  const before = attempts

  await context.addCookies(memberCookies)
  const reopened = await context.newPage()
  await reopened.goto(list, { waitUntil: 'commit' })
  await expect(reopened.getByText(title, { exact: true })).toBeVisible()
  const oldDatabase = `flow-ls:${encodeURIComponent(owner.id)}:${encodeURIComponent(workspace.id)}`
  await expect.poll(() => reopened.evaluate(async name => (await indexedDB.databases()).some(db => db.name === name), oldDatabase)).toBe(false)
  await expect(reopened.getByText(pendingTitle, { exact: true })).toHaveCount(0)
  expect(attempts).toBe(before)
  expect((await (await request.get(issueURL, { headers })).json()).title).toBe(title)
})

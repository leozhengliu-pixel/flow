import { expect } from '@playwright/test'
import { readLocalRow, signIn, test } from './fixture'

test.use({ workspaceNamePrefix: 'Partials-authority' })

test('never displays a retained history or archive page after team access is revoked', async ({ page, context, request, workspaceKey }) => {
  test.skip(process.env.FLOW_LOCAL_STORE_REAL_AUTH !== '1', 'Requires real browser sessions')
  const headers = { 'X-Workspace-Key': workspaceKey }
  const privateTeamResponse = await request.post(`/api/workspaces/${workspaceKey}/teams`, {
    headers, data: { name: 'Private slice fixtures', key: 'PVT', private: true },
  })
  expect(privateTeamResponse.status()).toBe(201)
  const team = await privateTeamResponse.json()
  const member = await signIn(context.request, 'member')
  const invitation = await request.post(`/api/workspaces/${workspaceKey}/invitations`, {
    data: { emails: [member.email], role: 'member', teamIds: [team.id] },
  })
  expect(invitation.status()).toBe(201)
  const [{ token }] = await invitation.json()
  expect((await context.request.post('/api/invitations/accept', { data: { token } })).ok()).toBe(true)
  for (const client of [request, context.request]) {
    expect((await client.patch('/api/account/settings', { headers, data: { pulseSchedule: 'never', language: 'en-US' } })).ok()).toBe(true)
  }
  const { states } = await (await request.get('/api/bootstrap', { headers })).json() as { states: Array<{ id: string; type: string; teamId?: string }> }
  const state = states.find(state => state.teamId === team.id && state.type === 'unstarted') ?? states.find(state => !state.teamId && state.type === 'unstarted')
  expect(state).toBeDefined()
  const create = async (title: string) => {
    const response = await request.post('/api/issues', { headers, data: { title, teamId: team.id, stateId: state!.id } })
    expect(response.status()).toBe(201)
    return response.json()
  }
  const issue = await create('Private activity fixture')
  const archived = await create('Private archive fixture')
  expect((await request.patch(`/api/issues/${archived.id}`, { headers, data: { archived: true } })).ok()).toBe(true)
  const body = 'Private comment kept in the retained slice'
  expect((await request.post(`/api/issues/${issue.id}/comments`, { headers, data: { body } })).status()).toBe(201)
  const teamPath = `/${workspaceKey}/team/pvt`
  const issuePath = `/${workspaceKey}/issue/${issue.identifier}/private-activity-fixture`
  const historyKey = `history:issue:${issue.id}`
  const archiveKey = `archive:team:${team.id}:50:`
  await page.goto(`${teamPath}/all`)
  // The shadow store can contain an issue before the resumable read model
  // has finished verifying its baseline. Start from the read model.
  await expect.poll(() => readLocalRow(page, 'workspace', '_meta')).toMatchObject({ resume: { schema: 2 } })
  await expect.poll(() => page.evaluate(() => performance.getEntriesByName('flow:local-store:checksum-match').length)).toBeGreaterThan(0)
  await expect.poll(() => readLocalRow(page, issue.id)).toMatchObject({ title: issue.title })
  const row = page.locator(`[data-issue-row-id="${issue.id}"]`)
  await expect(row).toBeVisible()
  await row.click()
  await expect(page.getByText(body, { exact: true })).toBeVisible()
  await expect.poll(() => readLocalRow(page, historyKey, '_partial_activity')).toMatchObject({ key: historyKey })
  await page.goto(`${teamPath}/archive/issues`)
  await expect(page.getByText(`${archived.identifier} ${archived.title}`, { exact: true })).toBeVisible()
  await expect.poll(() => readLocalRow(page, archiveKey, '_partial_issue')).toMatchObject({ key: archiveKey })
  await page.close()

  expect((await request.put(`/api/workspaces/${workspaceKey}/teams/${team.id}/members/${member.id}`, { headers, data: { member: false } })).ok()).toBe(true)
  await context.addInitScript(titles => {
    localStorage.setItem('flow:locale', 'en-US')
    new MutationObserver(records => {
      for (const record of records) {
        const nodes = record.type === 'characterData' ? [record.target] : Array.from(record.addedNodes)
        if (nodes.some(node => titles.some(title => node.textContent?.includes(title)))) {
          document.documentElement.dataset.privateSlicePainted = 'true'
        }
      }
    }).observe(document, { subtree: true, childList: true, characterData: true })
  }, [issue.title, body, archived.title])
  for (const [path, denial] of [[issuePath, 'Issue not found'], [`${teamPath}/archive/issues`, 'Team not found']]) {
    const reopened = await context.newPage()
    await reopened.goto(path, { waitUntil: 'commit' })
    await expect(reopened.getByText(denial, { exact: true })).toBeVisible()
    await expect.poll(() => readLocalRow(reopened, historyKey, '_partial_activity')).toBeUndefined()
    await expect.poll(() => readLocalRow(reopened, archiveKey, '_partial_issue')).toBeUndefined()
    await expect(reopened.locator('html')).not.toHaveAttribute('data-private-slice-painted', 'true')
    await reopened.close()
  }
})

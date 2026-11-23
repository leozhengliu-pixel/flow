import { expect, type Page, type Response } from '@playwright/test'
import { localStoreOwnerName } from '../../src/lib/local-store-notice'
import { readLocalRow, signIn, test } from './fixture'

test.use({ workspaceNamePrefix: 'Resume-authority' })

const changes = (response: Response) => new URL(response.url()).pathname === '/api/sync/changes' && response.url().includes('mode=issues')
const resumeCount = (page: Page) => page.evaluate(() => performance.getEntriesByName('flow:local-store:resume').length)
const databases = (page: Page) => page.evaluate(async () => (await indexedDB.databases()).filter(db => db.name?.startsWith('flow-ls:')).length)

test('resume clears lost team, issue, workspace and session access with real cookies', async ({ context, request, page, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const { teams, workspace } = await (await request.get('/api/bootstrap', { headers })).json()
  const team = teams[0]
  const teamResponse = await request.post(`/api/workspaces/${workspaceKey}/teams`, { headers, data: { name: 'Private fixtures', key: 'PVT', private: true } })
  expect(teamResponse.status()).toBe(201)
  const privateTeam = await teamResponse.json()
  const member = await signIn(context.request, 'member')
  const memberCookies = (await context.storageState()).cookies
  const invite = await request.post(`/api/workspaces/${workspaceKey}/invitations`, {
    data: { emails: [member.email], role: 'member', teamIds: [team.id, privateTeam.id] },
  })
  expect(invite.status()).toBe(201)
  const [{ token }] = await invite.json()
  expect((await context.request.post('/api/invitations/accept', { data: { token } })).status()).toBe(200)
  for (const client of [request, context.request]) {
    expect((await client.patch('/api/account/settings', { headers, data: { pulseSchedule: 'never', timezone: 'UTC' } })).status()).toBe(200)
  }
  // The fixture already set the timezone. A redundant startup write can
  // advance the bounded metadata revision while the private issue is seeded.
  await context.addInitScript(({ workspaceKey, viewerId }) => {
    sessionStorage.setItem(`flow.timezone-saved:${workspaceKey}:${viewerId}`, 'UTC')
  }, { workspaceKey, viewerId: member.id })

  const bootstrap = await request.get('/api/bootstrap', { headers })
  expect(bootstrap.ok()).toBe(true)
  const { states } = await bootstrap.json() as { states: Array<{ id: string; teamId?: string; type: string }> }
  const seed = async (title: string, teamId = team.id) => {
    const hasTeamStates = states.some(state => state.teamId === teamId)
    const state = states.find(state => state.type === 'unstarted' && (hasTeamStates ? state.teamId === teamId : !state.teamId))
    expect(state).toBeDefined()
    const response = await request.post('/api/issues', { headers, data: { teamId, title, stateId: state!.id } })
    expect(response.status()).toBe(201)
    return await response.json() as { id: string; identifier: string; title: string }
  }
  const keep = await seed('Retained public issue')
  const moved = await seed('Moved to private')
  const deleted = await seed('Deleted while closed')
  const projectedLost = await seed('Moved while a detail is open')
  const lost = await seed('Private before revocation', privateTeam.id)
  const path = `/${workspaceKey}/team/${team.key.toLowerCase()}/all`
  const privatePath = `/${workspaceKey}/team/pvt/all`

  const key = await request.post('/api/api-keys', { headers, data: { name: 'Team scoped fixture', scopes: ['read'], teamIds: [team.id] } })
  expect(key.status()).toBe(201)
  const { secret } = await key.json()
  const scopedHeaders = { ...headers, Authorization: `Bearer ${secret}`, 'X-Flow-Local-Store': member.id }
  const scopedQuery = new URLSearchParams({ surface: 'local-store', mode: 'issues', workspaceId: workspace.id, authzEpoch: '0', after: '0' })
  for (const endpoint of ['manifest', 'changes', 'records']) {
    expect((await context.request.get(`/api/sync/${endpoint}?${scopedQuery}`, { headers: scopedHeaders })).status()).toBe(403)
  }
  expect((await context.request.post(`/api/sync/batch?${scopedQuery}`, {
    headers: scopedHeaders, data: { projection: 'summary', keys: [{ kind: 'issues', id: keep.id }] },
  })).status()).toBe(403)
  await page.goto(path, { waitUntil: 'domcontentloaded' })
  await expect(page.getByText(keep.title, { exact: true })).toBeVisible()
  await expect.poll(() => readLocalRow(page, lost.id)).toMatchObject({ title: lost.title })
  expect(await readLocalRow(page, 'workspace', '_meta')).toHaveProperty('resume')
  await page.close()

  const warm = await context.newPage()
  await warm.goto(privatePath, { waitUntil: 'domcontentloaded' })
  await expect(warm.getByText(lost.title, { exact: true })).toBeVisible()
  await expect.poll(() => resumeCount(warm)).toBe(1)
  // The second tab retains the same epoch. Both must drop revoked rows.
  const sibling = await context.newPage()
  await sibling.goto(path, { waitUntil: 'domcontentloaded' })
  await expect(sibling.getByText(keep.title, { exact: true })).toBeVisible()
  await expect.poll(() => resumeCount(sibling)).toBe(1)
  expect((await request.put(`/api/workspaces/${workspaceKey}/teams/${privateTeam.id}/members/${member.id}`, { headers, data: { member: false } })).ok()).toBe(true)
  // Only one tab gets a wakeup; the sibling must see the storage invalidation.
  await warm.evaluate(() => window.dispatchEvent(new Event('online')))
  await expect(warm.getByText(lost.title, { exact: true })).toHaveCount(0)
  await expect.poll(() => readLocalRow(sibling, lost.id)).toBeUndefined()
  await warm.close()
  await sibling.close()

  // Mutations performed while the browser is closed still invalidate an
  // authorized baseline, without returning a key for the now-private issue.
  const retained = await context.newPage()
  await retained.goto(path, { waitUntil: 'domcontentloaded' })
  await expect(retained.getByText(moved.title, { exact: true })).toBeVisible()
  await expect.poll(() => readLocalRow(retained, moved.id)).toMatchObject({ title: moved.title })
  expect(await readLocalRow(retained, 'workspace', '_meta')).toHaveProperty('resume')
  await retained.close()
  expect((await request.patch(`/api/issues/${moved.id}`, { headers, data: { teamId: privateTeam.id } })).status()).toBe(200)
  expect((await request.delete(`/api/issues/${deleted.id}`, { headers })).ok()).toBe(true)
  const reopened = await context.newPage()
  await reopened.addInitScript(titles => {
    const observer = new MutationObserver(records => {
      if (records.some(record => Array.from(record.addedNodes).some(node => titles.some(title => node.textContent?.includes(title))))) {
        document.documentElement.dataset.revokedIssuePainted = 'true'
      }
    })
    observer.observe(document, { childList: true, subtree: true })
  }, [lost.title, moved.title, deleted.title])
  const recovery = reopened.waitForResponse(changes)
  await reopened.goto(path, { waitUntil: 'domcontentloaded' })
  expect((await recovery).ok()).toBe(true)
  await expect(reopened.getByText(keep.title, { exact: true })).toBeVisible()
  for (const issue of [lost, moved, deleted]) {
    await expect(reopened.getByText(issue.title, { exact: true })).toHaveCount(0)
    expect(await readLocalRow(reopened, issue.id)).toBeUndefined()
  }
  await expect.poll(() => reopened.evaluate(() =>
    performance.getEntriesByName('flow:local-store:checksum-match').length)).toBeGreaterThan(0)
  await expect.poll(() => readLocalRow(reopened, keep.id)).toMatchObject({ title: keep.title })
  expect(await reopened.locator('html').getAttribute('data-revoked-issue-painted')).toBeNull()
  await reopened.close()

  const detail = await context.newPage()
  const detailAuthority = detail.waitForResponse(changes)
  await detail.goto(`/${workspaceKey}/issue/${keep.identifier}/retained-public-issue`)
  await expect(detail.getByRole('textbox', { name: 'Issue title', exact: true })).toHaveText(keep.title)
  expect((await detailAuthority).ok()).toBe(true)
  await expect.poll(() => readLocalRow(detail, projectedLost.id)).toMatchObject({ title: projectedLost.title })
  expect((await request.patch(`/api/issues/${projectedLost.id}`, { headers, data: { teamId: privateTeam.id } })).ok()).toBe(true)
  await detail.evaluate(() => window.dispatchEvent(new Event('online')))
  await expect.poll(() => readLocalRow(detail, projectedLost.id)).toBeUndefined()
  await detail.close()

  // Hold the Web Lock without draining the journal, as a suspended tab can.
  const lock = localStoreOwnerName({ viewerId: member.id, workspaceId: workspace.id, workspaceKey })
  const pausedOwner = await context.newPage()
  await pausedOwner.goto('/favicon.svg')
  await pausedOwner.evaluate(name => new Promise<void>((resolve, reject) => {
    void navigator.locks.request(name, { signal: AbortSignal.timeout(5000) }, () => {
      resolve()
      return new Promise(() => {})
    }).catch(reject)
  }), lock)
  const lastMemberVisit = await context.newPage()
  await lastMemberVisit.goto(path, { waitUntil: 'domcontentloaded' })
  await expect(lastMemberVisit.getByText(keep.title, { exact: true })).toBeVisible()
  await expect.poll(() => lastMemberVisit.evaluate(() =>
    performance.getEntriesByName('flow:local-store:checksum-match').length)).toBeGreaterThan(0)
  await expect.poll(() => lastMemberVisit.evaluate(async name =>
    (await navigator.locks.query()).pending?.some(lock => lock.name === name), lock)).toBe(true)
  const refused = lastMemberVisit.waitForResponse(response => changes(response) && [403, 404].includes(response.status()), { timeout: 8000 })
  expect((await request.delete(`/api/workspaces/${workspaceKey}/members/${member.id}`, { headers })).ok()).toBe(true)
  await lastMemberVisit.evaluate(() => window.dispatchEvent(new Event('online')))
  await refused
  await expect.poll(() => databases(lastMemberVisit)).toBe(0)
  await expect(lastMemberVisit.getByText(keep.title, { exact: true })).toHaveCount(0)
  await lastMemberVisit.close()
  await pausedOwner.close()

  const ownerCookies = (await request.storageState()).cookies
  await context.addCookies(ownerCookies)
  const owner = await context.newPage()
  await owner.goto(path, { waitUntil: 'domcontentloaded' })
  await expect(owner.getByText(keep.title, { exact: true })).toBeVisible()
  await expect.poll(() => readLocalRow(owner, moved.id)).toMatchObject({ title: moved.title })
  await expect.poll(() => owner.evaluate(() => performance.getEntriesByName('flow:local-store:checksum-match').length)).toBeGreaterThan(0)
  // Any API refusal can expire the session first and cancel the journal read.
  const unauthorized = owner.waitForResponse(response => new URL(response.url()).pathname.startsWith('/api/') &&
    response.status() === 401, { timeout: 8000 })
  await context.clearCookies()
  await owner.evaluate(() => window.dispatchEvent(new Event('online')))
  await unauthorized
  await expect.poll(() => databases(owner)).toBe(0)
  await expect(owner.getByText(keep.title, { exact: true })).toHaveCount(0)
  await owner.close()

  await context.addCookies(ownerCookies)
  const switched = await context.newPage()
  await switched.goto(path, { waitUntil: 'commit' })
  await expect.poll(() => readLocalRow(switched, moved.id)).toMatchObject({ title: moved.title })
  await context.clearCookies()
  await switched.evaluate(() => window.dispatchEvent(new Event('online')))
  await expect.poll(() => databases(switched)).toBe(0)
  // The previous document may still be redirecting to sign-in. Open a new
  // document in the same context so that redirect cannot cancel this visit.
  await switched.close()
  await context.addCookies(memberCookies)
  const afterSwitch = await context.newPage()
  const checkedAccount = afterSwitch.waitForResponse(response => new URL(response.url()).pathname === '/api/account/bootstrap')
  await afterSwitch.goto(path, { waitUntil: 'domcontentloaded' })
  const account = await checkedAccount
  expect(account.ok()).toBe(true)
  expect((await account.json()).viewer.id).toBe(member.id)
  await expect.poll(() => databases(afterSwitch)).toBe(0)
  await expect(afterSwitch.getByText(keep.title, { exact: true })).toHaveCount(0)
})

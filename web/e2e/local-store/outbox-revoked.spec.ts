import { expect } from '@playwright/test'
import { editIssueTitle, interruptIssueWrites, readLocalRow, signIn, test } from './fixture'

test.use({ workspaceNamePrefix: 'Outbox-revoked' })

test('drops a queued edit when the viewer loses access to the issue', async ({ context, request, page, workspaceKey }) => {
  test.skip(process.env.FLOW_LOCAL_STORE_REAL_AUTH !== '1', 'Requires real browser sessions')
  const headers = { 'X-Workspace-Key': workspaceKey }
  const { workspace, teams: [team] } = await (await request.get('/api/bootstrap', { headers })).json()
  const privateTeam = await request.post(`/api/workspaces/${workspaceKey}/teams`, {
    headers, data: { name: 'Private fixtures', key: 'PVT', private: true },
  })
  expect(privateTeam.status()).toBe(201)
  const { id: privateTeamId } = await privateTeam.json()
  const member = await signIn(context.request, 'member')
  const invite = await request.post(`/api/workspaces/${workspaceKey}/invitations`, {
    data: { emails: [member.email], role: 'member', teamIds: [team.id] },
  })
  expect(invite.status()).toBe(201)
  const [{ token }] = await invite.json()
  expect((await context.request.post('/api/invitations/accept', { data: { token } })).ok()).toBe(true)
  const timezone = await page.evaluate(() => Intl.DateTimeFormat().resolvedOptions().timeZone)
  expect((await context.request.patch('/api/account/settings', { headers, data: { language: 'en-US', pulseSchedule: 'never', timezone } })).ok()).toBe(true)

  const create = async (title: string, client = request) => {
    const response = await client.post('/api/issue-records', { headers, data: { title, teamId: team.id } })
    expect(response.status()).toBe(201)
    return await response.json() as { id: string; title: string }
  }
  const issue = await create('Issue before access changed', context.request)
  const keep = await create('Permitted public issue')
  const path = `/api/issue-records/${issue.id}`
  const list = `/${workspaceKey}/team/${team.key.toLowerCase()}/all`
  let pendingId = ''
  let canReplay = false
  const epochs: string[] = []
  const epoch = (value: string | null | undefined) => {
    if (!value) return undefined
    if (!epochs.includes(value)) epochs.push(value)
    return epochs.indexOf(value)
  }
  await interruptIssueWrites(context, path, (_, id) => {
    if (id) pendingId = id
    return canReplay ? 'send' : 'disconnect'
  })
  const activity: string[] = []
  context.on('response', response => {
    const { pathname, searchParams } = new URL(response.url())
    const method = response.request().method()
    if (pathname.startsWith('/api/') && (method !== 'GET' ||
      /^\/api\/(?:sync|auth\/session|account\/bootstrap|issue-records|issues|bootstrap)/.test(pathname))) {
      activity.push(`${method} ${pathname} ${response.status()}`)
      if (pathname.startsWith('/api/sync/') && pathname !== '/api/sync/records') void response.json().then(data => {
        activity.push(JSON.stringify({ pathname, mode: searchParams.get('mode'), after: searchParams.get('after'),
          sentEpoch: epoch(searchParams.get('authzEpoch')), receivedEpoch: epoch(data.authzEpoch), revision: data.revision, head: data.head,
          resync: data.resyncRequired, invalidations: data.transactions?.flatMap((entry: { invalidations?: string[] }) => entry.invalidations ?? []) }))
      }).catch(() => undefined)
    }
  })
  await page.goto(list, { waitUntil: 'commit' })
  await expect.poll(() => readLocalRow(page, 'workspace', '_meta', workspace.id)).toHaveProperty('resume')
  await expect.poll(() => page.evaluate(() => performance.getEntriesByName('flow:local-store:checksum-match').length)).toBeGreaterThan(0)
  await expect.poll(() => page.evaluate(async () => (await navigator.locks.query()).held?.filter(lock => lock.name.startsWith('flow-sync:')).length)).toBe(1)
  await page.getByText(issue.title, { exact: true }).click()
  const title = page.getByRole('textbox', { name: 'Issue title', exact: true })
  await expect(title).toHaveText(issue.title)
  const queuedTitle = 'Title the viewer can no longer change'
  await editIssueTitle(title, queuedTitle)
  try {
    await expect.poll(() => pendingId).toMatch(/^[0-9a-f-]{36}$/)
  } catch (error) {
    const marks = await page.evaluate(() => performance.getEntriesByType('mark').map(entry => entry.name))
    const saveState = await page.locator('.issue-header .save-state').allTextContents()
    const local = await page.evaluate(async () => ({
      stores: (await indexedDB.databases()).filter(db => db.name?.startsWith('flow-ls:')).length,
      entry: Object.keys(localStorage).filter(key => key.startsWith('flow:local-store:flow-ls:')).map(key => JSON.parse(localStorage.getItem(key) ?? 'null')?.deleting === true),
      locks: (await navigator.locks.query()).held?.map(lock => lock.name?.split(':')[0]),
      urlKind: location.pathname.split('/')[2],
    }))
    console.log({ activity, marks, saveState, local })
    throw error
  }
  await expect.poll(() => readLocalRow(page, pendingId, '_outbox', workspace.id)).toMatchObject({ input: { title: queuedTitle } })
  const before = await readLocalRow(page, 'workspace', '_meta', workspace.id) as unknown as { authzEpoch: string; head: string; resume: { revision: string } }
  await page.close()

  expect((await request.patch(`/api/issues/${issue.id}`, { headers, data: { teamId: privateTeamId } })).ok()).toBe(true)
  const manifest = await context.request.get('/api/sync/manifest', {
    headers: { ...headers, 'X-Flow-Local-Store': member.id },
    params: { surface: 'local-store', mode: 'issues', workspaceId: workspace.id },
  })
  expect(manifest.ok()).toBe(true)
  const authority = await manifest.json()
  expect(authority.authzEpoch).toBe(before.authzEpoch)
  expect(BigInt(authority.head)).toBeGreaterThan(BigInt(before.head))
  expect(authority.revision).not.toBe(before.resume.revision)
  let rebuildStatus: number | undefined
  const reopened = await context.newPage()
  await reopened.exposeFunction('reportFixtureRebuild', (status: number) => { rebuildStatus = status })
  await reopened.addInitScript(revision => {
    const fixture = window as Window & { reportFixtureRebuild(status: number): Promise<void> }
    const fetch = window.fetch.bind(window)
    let stale = true
    window.fetch = async (input, init) => {
      if (!stale || typeof input !== 'string' || !input.startsWith('/api/sync/records?')) return fetch(input, init)
      const url = new URL(input, location.origin)
      if (url.searchParams.get('mode') !== 'issues') return fetch(input, init)
      stale = false
      // Ask the real server for the old revision to exercise a raced page.
      url.searchParams.set('revision', revision)
      const response = await fetch(url.pathname + url.search, init)
      await fixture.reportFixtureRebuild(response.status)
      return response
    }
  }, before.resume.revision)
  canReplay = true
  const replay = reopened.waitForResponse(
    response => response.url().endsWith(path) && response.request().method() === 'PATCH', { timeout: 15_000 },
  ).catch(error => ({ error }))
  await reopened.goto(list, { waitUntil: 'commit' })
  await expect(reopened.getByText(keep.title, { exact: true })).toBeVisible()
  const response = await replay
  if ('error' in response) {
    const state = await reopened.evaluate(async () => ({
      marks: performance.getEntriesByType('mark').map(entry => entry.name),
      owner: (await navigator.locks.query()).held?.some(lock => lock.name.startsWith('flow-sync:')),
    }))
    const checkpoint = await readLocalRow(reopened, 'workspace', '_meta', workspace.id) as { authzEpoch?: string } | undefined
    const queued = await readLocalRow(reopened, pendingId, '_outbox', workspace.id)
    console.log({ activity: activity.slice(-45), ...state, beforeEpoch: epoch(before.authzEpoch), currentEpoch: epoch(checkpoint?.authzEpoch),
      hasEdit: Boolean(queued), settled: queued && 'outcome' in queued })
    throw response.error
  }
  const status = response.status()
  expect([403, 404]).toContain(status)
  expect(rebuildStatus).toBe(409)
  await expect.poll(() => readLocalRow(reopened, pendingId, '_outbox', workspace.id)).toMatchObject({ outcome: { ok: false, status } })
  expect(await readLocalRow(reopened, pendingId, '_outbox', workspace.id)).not.toHaveProperty('input.title')
  expect(await readLocalRow(reopened, 'workspace', '_meta', workspace.id)).toMatchObject({ authzEpoch: before.authzEpoch })
  expect(await readLocalRow(reopened, issue.id, 'issue', workspace.id)).toBeUndefined()
  await expect(reopened.getByText(issue.title, { exact: true })).toHaveCount(0)
  await expect(reopened.getByText(queuedTitle, { exact: true })).toHaveCount(0)
  expect((await (await request.get(path, { headers })).json()).title).toBe(issue.title)
})

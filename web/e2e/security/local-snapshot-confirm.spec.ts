import { expect, test, type Page, type Route } from '@playwright/test'
import { apiFetch, signUp, startApi, type Account, type RunningApi } from './harness'

/**
 * A repeat visit paints from the local snapshot while the confirming requests are held; once they are released the
 * server's newer data must show on every kind of page, whatever the page copied from the snapshot when it mounted.
 */
let api: RunningApi
test.beforeAll(async () => { api = await startApi({ csp: 'enforce' }) })
test.afterAll(async () => { await api?.stop() })
test.use({ serviceWorkers: 'block', viewport: { width: 1440, height: 900 } })

type Team = { id: string; key: string }
type Slugged = { id: string; slugId: string }

async function snapshotSaved(page: Page) {
  await expect.poll(async () => page.evaluate(() => Object.keys(JSON.parse(localStorage.getItem('flow:local-snapshot') ?? '{}')).length), { timeout: 20_000 }).toBe(1)
}

/** Holds the confirming requests until released. */
async function holdConfirmation(page: Page) {
  const held: Route[] = []
  let open = false
  for (const pattern of ['**/api/bootstrap', '**/api/auth/session', '**/api/account/bootstrap']) {
    await page.route(pattern, route => { if (open) void route.continue(); else held.push(route) })
  }
  return {
    held: () => held.length,
    release: async () => { open = true; for (const route of held.splice(0)) await route.continue() },
  }
}

/** Opens `path` from the snapshot with the confirming requests held, runs the checks on the cached view, then releases. */
async function visitFromSnapshot(page: Page, path: string, cached: () => Promise<void>) {
  const requests = await holdConfirmation(page)
  await page.goto(path)
  await cached()
  expect(requests.held()).toBeGreaterThanOrEqual(3)
  return requests
}

async function patch(page: Page, account: Account, path: string, body: unknown) {
  const response = await apiFetch(page, 'PATCH', path, body, account.workspaceKey)
  expect(response.status, response.text).toBe(200)
}

test('pages painted from the snapshot show the data the server confirms', async ({ page, context }) => {
  const account = await signUp(context, api.origin, 'snapshot-confirm')
  const base = `${api.origin}/${account.workspaceKey}`
  await page.goto(`${api.origin}/sw.js`)
  const bootstrap = await apiFetch(page, 'GET', '/api/bootstrap', undefined, account.workspaceKey)
  expect(bootstrap.status).toBe(200)
  const team = (bootstrap.json!.teams as Team[])[0]
  const create = async (path: string, body: unknown) => {
    const response = await apiFetch(page, 'POST', path, body, account.workspaceKey)
    expect(response.status, response.text).toBe(201)
    return response.json as Slugged
  }
  await create('/api/issues', { title: 'Confirm seed issue', teamId: team.id })
  const document = await create('/api/documents', { title: 'Confirm document', content: 'Cached body text', teamIds: [team.id] })
  const project = await create('/api/projects', { name: 'Confirm project cached', teamIds: [team.id] })
  const initiative = await create('/api/initiatives', { name: 'Confirm initiative cached' })

  // Visit every page once, so the snapshot holds this state.
  await page.goto(`${base}/team/${team.key}/all`)
  await expect(page.getByText('Confirm seed issue')).toBeVisible()
  await expect(page.getByRole('list', { name: 'Issue board' })).toHaveCount(0)
  await snapshotSaved(page)

  // Before each page the server's data moves on (another member edits it), after the snapshot was taken.
  // Document: the cached copy has no body; the editor mounts from the server's.
  await patch(page, account, `/api/documents/${document.id}`, { content: 'Fresh body text' })
  let requests = await visitFromSnapshot(page, `${base}/document/${document.slugId}`, async () => {
    await expect(page.getByText('Confirm document').first()).toBeVisible()
  })
  await requests.release()
  await expect(page.getByRole('textbox', { name: 'Document content' })).toContainText('Fresh body text', { timeout: 20_000 })

  // Issue list: the team's shared default (board) arrives with the confirmation.
  await patch(page, account, `/api/teams/${team.id}/settings`, { issueViewDefaults: { all: { layout: 'board' } } })
  requests = await visitFromSnapshot(page, `${base}/team/${team.key}/all`, async () => {
    await expect(page.getByText('Confirm seed issue')).toBeVisible()
    await expect(page.getByRole('list', { name: 'Issue board' })).toHaveCount(0)
  })
  await requests.release()
  await expect(page.getByRole('list', { name: 'Issue board' })).toBeVisible({ timeout: 20_000 })

  // Project and initiative details.
  await patch(page, account, `/api/projects/${project.id}`, { name: 'Confirm project fresh' })
  requests = await visitFromSnapshot(page, `${base}/project/${project.slugId}/overview`, async () => {
    await expect(page.getByText('Confirm project cached').first()).toBeVisible()
  })
  await requests.release()
  await expect(page.getByText('Confirm project fresh').first()).toBeVisible({ timeout: 20_000 })
  await expect(page.getByText('Confirm project cached')).toHaveCount(0)

  await patch(page, account, `/api/initiatives/${initiative.id}`, { name: 'Confirm initiative fresh' })
  requests = await visitFromSnapshot(page, `${base}/initiative/${initiative.slugId}`, async () => {
    await expect(page.getByText('Confirm initiative cached').first()).toBeVisible()
  })
  await requests.release()
  await expect(page.getByText('Confirm initiative fresh').first()).toBeVisible({ timeout: 20_000 })
  await expect(page.getByText('Confirm initiative cached')).toHaveCount(0)

  // A settings form: the workspace name field.
  await patch(page, account, `/api/workspaces/${account.workspaceKey}`, { name: 'Renamed workspace' })
  requests = await visitFromSnapshot(page, `${base}/settings/workspace`, async () => {
    await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toBeVisible()
  })
  await requests.release()
  await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue('Renamed workspace', { timeout: 20_000 })
})

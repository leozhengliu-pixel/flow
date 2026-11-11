import { expect, test, type Page } from '@playwright/test'
import { apiFetch, signIn, signUp, startApi, type Account } from '../security/harness'

/**
 * An editor's document edit, project comment and project update reach a viewer's open tab through realtime events
 * applied in place: the viewer never downloads the workspace metadata bootstrap for them. An access change (the viewer
 * joins a team) still reloads the metadata, once.
 */

function bootstrapRequests(page: Page) {
  const seen: string[] = []
  page.on('request', request => {
    const url = new URL(request.url())
    if (url.pathname === '/api/issue-records/bootstrap' || url.pathname === '/api/bootstrap') seen.push(`${request.method()} ${url.pathname}`)
  })
  return seen
}

async function json<T>(page: Page, method: string, path: string, body: unknown, workspaceKey: string, status: number): Promise<T> {
  const response = await apiFetch(page, method, path, body, workspaceKey)
  expect(response.status, `${method} ${path}: ${response.text}`).toBe(status)
  return response.json as T
}

test('document, project comment and project update edits reach a viewer without a metadata reload', async ({ browser, context, page }) => {
  const api = await startApi({ csp: 'off' })
  try {
    const editor: Account = await signUp(context, api.origin, 'realtime-editor')
    const key = editor.workspaceKey
    await page.goto(`${api.origin}/sw.js`)
    const teams = await json<{ nodes: Array<{ id: string; key: string }> }>(page, 'GET', '/api/teams', undefined, key, 200)
    const team = teams.nodes[0]
    expect(team).toBeTruthy()
    const accessTeam = await json<{ id: string }>(page, 'POST', `/api/workspaces/${key}/teams`, { name: 'Access team', key: 'ACC' }, '', 201)
    const document = await json<{ id: string; slugId: string }>(page, 'POST', '/api/documents', { title: 'Shared spec', content: 'First draft' }, key, 201)
    const project = await json<{ id: string; slugId: string }>(page, 'POST', '/api/projects', { name: 'Realtime project', teamIds: [team.id] }, key, 201)

    // The viewer: a member of the workspace's first team, in a browser of their own.
    const viewerContext = await browser.newContext()
    const viewer = await viewerContext.newPage()
    await viewer.goto(`${api.origin}/sw.js`)
    const viewerAccount = { email: 'realtime-viewer@example.test', password: 'correct-horse-battery-1', workspaceKey: key }
    const registered = await apiFetch(viewer, 'POST', '/api/auth/register', { name: 'Realtime Viewer', email: viewerAccount.email, password: viewerAccount.password })
    expect(registered.status).toBe(201)
    expect((await apiFetch(viewer, 'POST', '/api/auth/verify-email', { token: registered.json?.verificationToken })).status).toBe(200)
    await signIn(viewer, viewerAccount)
    const invitations = await json<Array<{ token: string }>>(page, 'POST', `/api/workspaces/${key}/invitations`, { emails: [viewerAccount.email], role: 'member', teamIds: [team.id] }, '', 201)
    await json(viewer, 'POST', '/api/invitations/accept', { token: invitations[0].token }, '', 200)
    const viewerUser = (await json<{ user: { id: string } }>(viewer, 'GET', '/api/viewer', undefined, key, 200)).user

    // The viewer opens the document; once the page has settled, count metadata downloads.
    const requests = bootstrapRequests(viewer)
    await viewer.goto(`${api.origin}/${key}/document/${document.slugId}`)
    const title = viewer.getByRole('textbox', { name: 'Document title' })
    await expect(title).toHaveValue('Shared spec')
    await viewer.waitForLoadState('networkidle')
    await viewer.waitForTimeout(1500)
    requests.length = 0

    await json(page, 'PATCH', `/api/documents/${document.id}`, { title: 'Shared spec, revised' }, key, 200)
    await expect(title).toHaveValue('Shared spec, revised', { timeout: 15_000 })

    // The project's updates page lists its comments and updates.
    await viewer.goto(`${api.origin}/${key}/project/${project.slugId}/updates`)
    await expect(viewer.getByText('Realtime project').first()).toBeVisible()
    await viewer.waitForLoadState('networkidle')
    await viewer.waitForTimeout(1500)
    requests.length = 0
    await json(page, 'POST', `/api/projects/${project.id}/comments`, { body: 'A comment from the editor' }, key, 201)
    await expect(viewer.getByText('A comment from the editor').first()).toBeVisible({ timeout: 15_000 })
    await json(page, 'POST', `/api/projects/${project.id}/updates`, { body: 'An update from the editor', health: 'atRisk' }, key, 201)
    await expect(viewer.getByText('An update from the editor').first()).toBeVisible({ timeout: 15_000 })
    await viewer.waitForTimeout(1500)
    expect(requests, 'edits must not reload the workspace metadata').toEqual([])

    // An access change: the viewer joins another team. That reloads the metadata, once.
    const joined = await apiFetch(page, 'PUT', `/api/teams/${accessTeam.id}/members/${viewerUser.id}`, { role: 'member' }, key)
    expect(joined.status, joined.text).toBe(200)
    await expect.poll(() => requests.length, { timeout: 15_000 }).toBeGreaterThan(0)
    await viewer.waitForTimeout(3000)
    // One reload: the metadata projection (a paged client) or, for a client that holds every issue, the bootstrap
    // with the newly visible team's issues.
    expect(requests).toHaveLength(1)
    await viewerContext.close()
  } finally {
    await api.stop()
  }
})

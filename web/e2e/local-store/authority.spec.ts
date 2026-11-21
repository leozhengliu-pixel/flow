import { expect, type Page } from '@playwright/test'
import { readLocalRow, signIn, test } from './fixture'

test.use({ workspaceNamePrefix: 'Authority' })

function databases(page: Page) {
  return page.evaluate(async () => (await indexedDB.databases()).filter(db => db.name?.startsWith('flow-ls:')).length)
}

test('purges revoked team, workspace and session access and excludes team-restricted keys', async ({ request, context, page, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const owner = await request.get('/api/bootstrap', { headers })
  expect(owner.ok()).toBe(true)
  const { teams } = await owner.json()
  const privateTeamResponse = await request.post(`/api/workspaces/${workspaceKey}/teams`, {
    headers, data: { name: 'Private fixture team', key: 'PVT', private: true },
  })
  expect(privateTeamResponse.status()).toBe(201)
  const privateTeam = await privateTeamResponse.json()
  const member = await signIn(context.request, 'member')
  const invite = await request.post(`/api/workspaces/${workspaceKey}/invitations`, {
    data: { emails: [member.email], role: 'member', teamIds: [teams[0].id, privateTeam.id] },
  })
  expect(invite.status()).toBe(201)
  const [{ token }] = await invite.json()
  expect((await context.request.post('/api/invitations/accept', { data: { token } })).status()).toBe(200)
  for (const client of [request, context.request]) {
    const settings = await client.patch('/api/account/settings', { headers, data: { pulseSchedule: 'never', timezone: 'UTC' } })
    expect(settings.status()).toBe(200)
  }
  const title = 'Visible before revocation'
  const issue = await request.post('/api/issues', { headers, data: { teamId: teams[0].id, title } })
  expect(issue.status()).toBe(201)
  const publicIssue = await issue.json()
  const privateTitle = 'Private before team revocation'
  const privateIssueResponse = await request.post('/api/issues', { headers, data: { teamId: privateTeam.id, title: privateTitle } })
  expect(privateIssueResponse.status()).toBe(201)
  const privateIssue = await privateIssueResponse.json()

  const scoped = await request.post('/api/api-keys', { headers, data: { name: 'Scoped read fixture', scopes: ['read'], teamIds: [teams[0].id] } })
  expect(scoped.status()).toBe(201)
  const { secret } = await scoped.json()
  await page.route('**/api/bootstrap', async route => {
    const response = await route.fetch({ headers: { ...route.request().headers(), Authorization: `Bearer ${secret}` } })
    expect(response.status()).toBe(200)
    expect(response.headers()['x-flow-local-store']).toBeUndefined()
    await route.fulfill({ response })
  })
  const path = `/${workspaceKey}/team/${teams[0].key.toLowerCase()}/all`
  const scopedBootstrap = page.waitForResponse(response => new URL(response.url()).pathname === '/api/bootstrap')
  await page.goto(path)
  // Explicitly confirm the scoped bootstrap made no durable grant in this origin.
  expect((await scopedBootstrap).ok()).toBe(true)
  expect(await databases(page)).toBe(0)
  await page.unroute('**/api/bootstrap')
  await page.reload()
  await expect(page.getByText(title, { exact: true })).toBeVisible()
  await expect.poll(() => readLocalRow(page, privateIssue.id)).toMatchObject({ title: privateTitle })

  const openIssue = async () => {
    const detail = await context.newPage()
    const manifest = detail.waitForResponse(response => new URL(response.url()).searchParams.get('surface') === 'local-store' &&
      new URL(response.url()).pathname === '/api/sync/manifest')
    await detail.goto(`/${workspaceKey}/issue/${publicIssue.identifier}/visible-before-revocation`)
    await expect(detail.getByText(title, { exact: true }).first()).toBeVisible()
    expect((await manifest).status()).toBe(200)
    return detail
  }
  const setTeamMember = async (allowed: boolean) => {
    const response = await request.put(`/api/workspaces/${workspaceKey}/teams/${privateTeam.id}/members/${member.id}`, {
      headers, data: { member: allowed },
    })
    expect(response.ok()).toBe(true)
  }
  await page.close()
  const detail = await openIssue()
  await expect.poll(() => readLocalRow(detail, privateIssue.id)).toMatchObject({ title: privateTitle })
  await setTeamMember(false)
  await detail.evaluate(() => window.dispatchEvent(new Event('online')))
  await expect.poll(() => databases(detail)).toBe(0)
  await detail.close()

  // Restart directly on a projected route, without a running tab to process
  // realtime revocation and without a new full bootstrap to replace the rows.
  await setTeamMember(true)
  const reseeded = await context.newPage()
  await reseeded.goto(path)
  await expect.poll(() => readLocalRow(reseeded, privateIssue.id)).toMatchObject({ title: privateTitle })
  await reseeded.close()
  await setTeamMember(false)
  const afterRevoke = await openIssue()
  await expect.poll(() => databases(afterRevoke)).toBe(0)
  await afterRevoke.close()

  const reopened = await context.newPage()
  await reopened.goto(path)
  await expect(reopened.getByText(title, { exact: true })).toBeVisible()
  await expect.poll(() => readLocalRow(reopened, publicIssue.id)).toMatchObject({ title })
  expect(await readLocalRow(reopened, privateIssue.id)).toBeUndefined()

  const removed = await request.delete(`/api/workspaces/${workspaceKey}/members/${member.id}`, { headers })
  expect(removed.ok()).toBe(true)
  await reopened.evaluate(() => window.dispatchEvent(new Event('online')))
  await expect.poll(() => databases(reopened)).toBe(0)
  await reopened.reload()
  await expect(reopened.getByText(title, { exact: true })).toHaveCount(0)
  expect(await databases(reopened)).toBe(0)
  await reopened.close()

  await context.addCookies((await request.storageState()).cookies)
  const ownerPage = await context.newPage()
  await ownerPage.goto(path)
  await expect(ownerPage.getByText(title, { exact: true })).toBeVisible()
  await expect.poll(() => readLocalRow(ownerPage, publicIssue.id)).toMatchObject({ title })
  await context.clearCookies()
  const unauthorized = ownerPage.waitForResponse(response => new URL(response.url()).pathname === '/api/auth/session' && response.status() === 401)
  await ownerPage.reload()
  await unauthorized
  await expect(ownerPage.getByText(title, { exact: true })).toHaveCount(0)
  await expect.poll(() => databases(ownerPage)).toBe(0)
})

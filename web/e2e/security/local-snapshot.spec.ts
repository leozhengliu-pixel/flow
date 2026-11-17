import { expect, test, type BrowserContext, type Page, type Route } from '@playwright/test'
import { apiFetch, signIn, signUp, startApi, type Account, type RunningApi } from './harness'

/**
 * Repeat visits paint the workspace from the local snapshot (src/lib/local-snapshot.ts) before the session, account
 * and bootstrap requests answer, against the production build the Go server serves with sign-in on. The service
 * worker is kept out of these tests (service-worker.spec.ts covers it) so every request reaches page.route.
 */
let api: RunningApi
// Sign-ups are rate limited per client: each group of tests gets its own API.
function ownApi() {
  test.beforeAll(async () => { api = await startApi({ csp: 'enforce' }) })
  test.afterAll(async () => { await api?.stop() })
}
test.use({ serviceWorkers: 'block', viewport: { width: 1440, height: 900 } })

type Team = { id: string; key: string }

async function seedIssue(page: Page, account: Account, title: string) {
  await page.goto(`${api.origin}/sw.js`)
  const workspace = await apiFetch(page, 'GET', '/api/bootstrap', undefined, account.workspaceKey)
  expect(workspace.status).toBe(200)
  const team = (workspace.json!.teams as Team[])[0]
  const created = await apiFetch(page, 'POST', '/api/issues', { title, teamId: team.id }, account.workspaceKey)
  expect(created.status).toBe(201)
  return { team, issue: created.json as { id: string; identifier: string } }
}

function issuesPath(account: Account, team: Team) {
  return `${api.origin}/${account.workspaceKey}/team/${team.key}/all`
}

async function snapshotIndex(page: Page) {
  return page.evaluate(() => JSON.parse(localStorage.getItem('flow:local-snapshot') ?? '{}') as Record<string, string>)
}

async function snapshotRecords(page: Page) {
  return page.evaluate(() => new Promise<number>(resolve => {
    const open = indexedDB.open('flow-local-snapshot', 1)
    open.onupgradeneeded = () => { if (!open.result.objectStoreNames.contains('snapshots')) open.result.createObjectStore('snapshots', { keyPath: 'key' }) }
    open.onerror = () => resolve(-1)
    open.onsuccess = () => {
      const count = open.result.transaction('snapshots', 'readonly').objectStore('snapshots').count()
      count.onsuccess = () => { resolve(count.result); open.result.close() }
      count.onerror = () => { resolve(-1); open.result.close() }
    }
  }))
}

async function waitForSnapshot(page: Page) {
  await expect.poll(async () => Object.keys(await snapshotIndex(page)).length, { timeout: 15_000 }).toBe(1)
}

/** Holds the matching requests until released, recording when each one was made. */
async function holdRequests(page: Page, patterns: string[]) {
  const held: Route[] = []
  let open = false
  for (const pattern of patterns) {
    await page.route(pattern, route => { if (open) void route.continue(); else held.push(route) })
  }
  return {
    count: () => held.length,
    release: async () => {
      open = true
      for (const route of held.splice(0)) await route.continue()
    },
  }
}

/** Records, from the first script of every page, whether a text ever appeared in the document. */
async function watchForText(context: BrowserContext, text: string) {
  await context.addInitScript(needle => {
    const seen = () => { if (document.body?.innerText.includes(needle)) sessionStorage.setItem('flow-test:seen', '1') }
    new MutationObserver(seen).observe(document, { subtree: true, childList: true, characterData: true })
  }, text)
}

test.describe('one viewer', () => {
  ownApi()

  test('a repeat visit paints the workspace before the session, account and bootstrap requests answer, then fresh data replaces it', async ({ page, context }) => {
    const account = await signUp(context, api.origin, 'snapshot-paint')
    const { team, issue } = await seedIssue(page, account, 'Snapshot cached title')
    await page.goto(issuesPath(account, team))
    await expect(page.getByText('Snapshot cached title')).toBeVisible()
    await waitForSnapshot(page)
    expect(await snapshotRecords(page)).toBe(1)

    // The issue changes on the server after the snapshot was taken.
    expect((await apiFetch(page, 'PATCH', `/api/issues/${issue.id}`, { title: 'Snapshot fresh title' }, account.workspaceKey)).status).toBe(200)

    const requests = await holdRequests(page, ['**/api/bootstrap', '**/api/auth/session', '**/api/account/bootstrap'])
    await page.goto(issuesPath(account, team))
    // Painted from the snapshot while all three requests are still unanswered.
    await expect(page.getByText('Snapshot cached title')).toBeVisible()
    await expect(page.locator('.sidebar').first()).toBeVisible()
    expect(requests.count()).toBeGreaterThanOrEqual(3)

    await requests.release()
    await expect(page.getByText('Snapshot fresh title')).toBeVisible()
    await expect(page.getByText('Snapshot cached title')).toHaveCount(0)
    // The confirmed state is what the next visit starts from.
    await expect.poll(async () => page.evaluate(() => new Promise<string>(resolve => {
      const open = indexedDB.open('flow-local-snapshot', 1)
      open.onsuccess = () => {
        const all = open.result.transaction('snapshots', 'readonly').objectStore('snapshots').getAll()
        all.onsuccess = () => { resolve(String((all.result[0] as { json?: string } | undefined)?.json ?? '')); open.result.close() }
      }
    })), { timeout: 45_000 }).toContain('Snapshot fresh title')
  })

  test('writes made while the snapshot is unconfirmed wait for the fresh bootstrap and then save as usual', async ({ page, context }) => {
    const account = await signUp(context, api.origin, 'snapshot-write')
    const { team } = await seedIssue(page, account, 'Snapshot write seed')
    await page.goto(issuesPath(account, team))
    await expect(page.getByText('Snapshot write seed')).toBeVisible()
    await waitForSnapshot(page)

    const order: string[] = []
    page.on('request', request => {
      const path = new URL(request.url()).pathname
      if (path === '/api/issues' && request.method() === 'POST') order.push('create')
    })
    const requests = await holdRequests(page, ['**/api/bootstrap'])
    await page.goto(issuesPath(account, team))
    await expect(page.getByText('Snapshot write seed')).toBeVisible()
    await page.locator('.sidebar').click({ position: { x: 4, y: 50 } })
    await page.keyboard.press('c')
    const composer = page.locator('[data-flow-motion="create"]')
    await composer.locator('.title-editor').fill('Created while unconfirmed')
    await composer.getByRole('button', { name: 'Create issue', exact: true }).click()
    // Held: nothing is sent against the unconfirmed snapshot.
    await page.waitForTimeout(500)
    expect(order).toEqual([])
    const created = page.waitForResponse(response => new URL(response.url()).pathname === '/api/issues' && response.request().method() === 'POST')
    await requests.release()
    order.push('released')
    expect((await created).status()).toBe(201)
    await expect(page.getByText('Created while unconfirmed').first()).toBeVisible()
    const workspace = await apiFetch(page, 'GET', '/api/bootstrap', undefined, account.workspaceKey)
    expect((workspace.json!.issues as Array<{ title: string }>).map(item => item.title)).toContain('Created while unconfirmed')

    // Once confirmed, a write goes out at once, as it always did (creating opens the new issue; start from the list).
    await page.goto(issuesPath(account, team))
    await expect(page.getByText('Created while unconfirmed')).toBeVisible()
    await page.waitForTimeout(300)
    order.length = 0
    await page.keyboard.press('c')
    await composer.locator('.title-editor').fill('Created after confirmation')
    const second = page.waitForResponse(response => new URL(response.url()).pathname === '/api/issues' && response.request().method() === 'POST')
    await composer.getByRole('button', { name: 'Create issue', exact: true }).click()
    expect((await second).status()).toBe(201)
    expect(order).toEqual(['create'])
  })

  test('a refused bootstrap drops the cached view and its snapshot', async ({ page, context }) => {
    const account = await signUp(context, api.origin, 'snapshot-refused')
    const { team } = await seedIssue(page, account, 'Snapshot refused title')
    await page.goto(issuesPath(account, team))
    await expect(page.getByText('Snapshot refused title')).toBeVisible()
    await waitForSnapshot(page)

    const held: Route[] = []
    let refuse = false
    const deny = (route: Route) => route.fulfill({ status: 403, contentType: 'application/json', body: JSON.stringify({ error: 'forbidden' }) })
    await page.route('**/api/bootstrap', route => { if (refuse) void deny(route); else held.push(route) })
    await page.goto(issuesPath(account, team))
    await expect(page.getByText('Snapshot refused title')).toBeVisible()
    await expect.poll(() => held.length).toBeGreaterThan(0)
    // Every bootstrap is refused from now on (the speculative one is retried once as a normal request).
    refuse = true
    for (const route of held.splice(0)) await deny(route)
    await expect.poll(() => page.evaluate(() => document.body.innerText.includes('Snapshot refused title')), { timeout: 15_000 }).toBe(false)
    await expect(page.getByText('Snapshot refused title')).toHaveCount(0)
    await expect.poll(async () => Object.keys(await snapshotIndex(page)).length).toBe(0)
    // Today's answer to a refused workspace.
    await expect(page.getByText('This workspace does not exist.')).toBeVisible()
    await page.unroute('**/api/bootstrap')
  })
})

test.describe('two viewers', () => {
  ownApi()

  test('logging out deletes the snapshot, and the next viewer never sees it', async ({ page, context }) => {
    const account = await signUp(context, api.origin, 'snapshot-owner')
    const { team } = await seedIssue(page, account, 'Snapshot private title')
    await page.goto(issuesPath(account, team))
    await expect(page.getByText('Snapshot private title')).toBeVisible()
    await waitForSnapshot(page)

    await page.locator('.workspace-menu-trigger').click()
    await page.locator('.workspace-primary-menu').getByRole('menuitem', { name: /Log out/ }).click()
    await expect(page).toHaveURL(/\/login/)
    await expect.poll(async () => Object.keys(await snapshotIndex(page)).length).toBe(0)
    await expect.poll(() => snapshotRecords(page)).toBe(0)

    // Another person signs in on this browser and opens the same address.
    await watchForText(context, 'Snapshot private title')
    const other = await signUp(context, api.origin, 'snapshot-other')
    await signIn(page, other)
    const requests = await holdRequests(page, ['**/api/bootstrap', '**/api/auth/session', '**/api/account/bootstrap'])
    await page.goto(issuesPath(account, team))
    await page.waitForTimeout(500)
    await requests.release()
    await expect(page.locator('main').first()).toBeVisible()
    await page.waitForTimeout(500)
    expect(await page.evaluate(() => sessionStorage.getItem('flow-test:seen'))).toBeNull()
  })

  test('a session that ended without a logout here keeps its snapshot from the next viewer who signs in', async ({ page, context }) => {
    const account = await signUp(context, api.origin, 'snapshot-expired')
    const { team } = await seedIssue(page, account, 'Snapshot expired title')
    await page.goto(issuesPath(account, team))
    await expect(page.getByText('Snapshot expired title')).toBeVisible()
    await waitForSnapshot(page)

    // The session ends on the server side (no logout in this browser); someone else signs in through the app.
    await context.clearCookies()
    const other = await signUp(context, api.origin, 'snapshot-next')
    await context.clearCookies()
    await page.goto(`${api.origin}/login`)
    await page.getByLabel(/email/i).first().fill(other.email)
    await page.getByLabel(/password/i).first().fill(other.password)
    await page.getByRole('button', { name: /^(Continue|Log in|Sign in)/ }).first().click()
    await expect(page).not.toHaveURL(/\/login/)
    await expect.poll(async () => Object.keys(await snapshotIndex(page)).filter(key => key.includes(encodeURIComponent(account.workspaceKey))).length).toBe(0)

    await watchForText(context, 'Snapshot expired title')
    await page.goto(`${api.origin}/${account.workspaceKey}`)
    await page.waitForTimeout(1000)
    expect(await page.evaluate(() => sessionStorage.getItem('flow-test:seen'))).toBeNull()
  })
})

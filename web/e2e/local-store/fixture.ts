import { expect, type APIRequestContext, type BrowserContext, type Locator, type Page } from '@playwright/test'
import { test as workspace } from '../workspace-fixture'

// Start a real reload and wait for its document. Waiting for all assets can
// race the engine when it cancels an old navigation's outstanding requests.
export async function reloadDocument(page: Page) {
  const navigated = page.waitForEvent('framenavigated', frame => frame === page.mainFrame())
  await page.evaluate(() => { window.setTimeout(() => window.location.reload(), 0) })
  await navigated
}

export async function editIssueTitle(title: Locator, value: string) {
  await title.focus()
  await title.press('ControlOrMeta+a')
  await title.page().keyboard.insertText(value)
  await expect(title).toHaveText(value)
  await title.blur()
}

type WriteDecision = 'send' | 'disconnect' | 'lose-response'

export async function interruptIssueWrites(
  context: BrowserContext,
  path: string,
  decide: (page: Page, mutationId: string) => WriteDecision,
  committed: (status: number) => void = () => {},
) {
  await context.exposeBinding('fixtureIssueWrite', ({ page }, id: string) => decide(page, id))
  await context.exposeBinding('fixtureIssueResponse', (_, status: number) => committed(status))
  // Simulate transport loss around the real fetch, including a committed write
  // whose response never reaches the client.
  await context.addInitScript(path => {
    const fixture = window as Window & {
      fixtureIssueWrite(id: string): Promise<WriteDecision>
      fixtureIssueResponse(status: number): Promise<void>
    }
    const fetch = window.fetch.bind(window)
    window.fetch = async (input, init) => {
      if (input !== path || init?.method !== 'PATCH') return fetch(input, init)
      const id = new Headers(init.headers).get('X-Flow-Local-Mutation') ?? ''
      const decision = await fixture.fixtureIssueWrite(id)
      if (decision === 'disconnect') throw new TypeError('Fixture connection dropped')
      const response = await fetch(input, init)
      if (decision === 'lose-response') {
        await response.arrayBuffer()
        await fixture.fixtureIssueResponse(response.status)
        throw new TypeError('Fixture response lost')
      }
      return response
    }
  }, path)
}

export function readLocalRow(page: Page, id: string, store = 'issue', workspaceId?: string) {
  return page.evaluate(async ({ id, store, workspaceId }) => {
    const { name } = (await indexedDB.databases()).find(db => db.name?.startsWith('flow-ls:') &&
      (!workspaceId || db.name.endsWith(`:${encodeURIComponent(workspaceId)}`))) ?? {}
    if (!name) return undefined
    return new Promise<{ id: string; title: string; _seq: string; _v: string | null } | undefined>((resolve, reject) => {
      const open = indexedDB.open(name)
      open.onupgradeneeded = () => open.transaction?.abort()
      open.onerror = () => open.error?.name === 'AbortError' ? resolve(undefined) : reject(open.error)
      open.onsuccess = () => {
        const db = open.result
        const request = db.transaction(store, 'readonly').objectStore(store).get(id)
        request.onsuccess = () => resolve(request.result)
        request.onerror = () => reject(request.error)
        db.close()
      }
    })
  }, { id, store, workspaceId })
}

export async function signIn(request: APIRequestContext, role: 'owner' | 'member' = 'owner') {
  const email = `local-store-${role}@example.test`
  const password = 'test-local-store-fixture-1'
  let login = await request.post('/api/auth/login', { data: { email, password } })
  // All three projects use this scratch API. Reuse the identities so the
  // third project stays inside the signup limit while creating its own workspace.
  if (login.status() === 401) {
    const register = await request.post('/api/auth/register', { data: { name: 'Storage tester', email, password } })
    expect(register.status()).toBe(201)
    const { verificationToken } = await register.json()
    expect((await request.post('/api/auth/verify-email', { data: { token: verificationToken } })).status()).toBe(200)
    login = await request.post('/api/auth/login', { data: { email, password } })
  }
  expect(login.status()).toBe(200)
  const { user } = await login.json()
  return { id: user.id as string, email }
}

export const test = workspace.extend({
  workspaceURLKey: async ({ workspaceNamePrefix }, provide, info) => {
    await provide(`browser-local-store-${info.project.name}-${workspaceNamePrefix.toLowerCase()}`)
  },
  request: async ({ request }, provide) => {
    if (process.env.FLOW_LOCAL_STORE_REAL_AUTH === '1') await signIn(request)
    await provide(request)
  },
})

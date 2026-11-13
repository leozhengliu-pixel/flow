import { expect, type Page } from '@playwright/test'
import { issuePath } from '../../src/lib/app-routes'
import { test as workspaceTest } from '../workspace-fixture'
import { apiFetch, signUp, startApi, type Account, type RunningApi } from '../security/harness'

const test = workspaceTest.extend<{ api: RunningApi; owner: Account }>({
  api: async ({ browserName }, provide) => { expect(browserName).toBe('chromium'); const api = await startApi({ csp: 'off', env: { FLOW_PRESENCE_MINIMAL: 'on' } }); try { await provide(api) } finally { await api.stop() } },
  baseURL: async ({ api }, provide) => { await provide(api.origin) },
  owner: async ({ context, api }, provide) => { await provide(await signUp(context, api.origin, 'p06-owner')) },
  request: async ({ context, owner }, provide) => { expect(owner.email).toContain('p06-owner'); await provide(context.request) },
})

type PresenceSnapshot = { types: string[]; roster: Array<{ clientId: string; issueId?: string; route?: string }> }

async function observePresence(page: Page) {
  await page.addInitScript(() => {
    const snapshot: PresenceSnapshot = { types: [], roster: [] }
    Object.assign(window, { scaleP06Presence: snapshot })
    const NativeEventSource = window.EventSource
    window.EventSource = class extends NativeEventSource {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options)
        this.addEventListener('message', message => {
          const event = JSON.parse(message.data) as { type: string; payload?: { presence?: PresenceSnapshot['roster'] } }
          if (event.payload?.presence) {
            snapshot.types.push(event.type)
            snapshot.roster = event.payload.presence
          }
        })
      }
    }
  })
}

async function presenceSnapshot(page: Page) {
  return page.evaluate(() => (window as unknown as { scaleP06Presence: PresenceSnapshot }).scaleP06Presence)
}

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  test.describe(`${viewport.width}px viewers`, () => {
    test.use({ viewport, workspaceNamePrefix: 'P06 viewers' })
    test('another user joins, moves and leaves with 204 heartbeats', async ({ api, browser, page, workspaceKey }) => {
      test.setTimeout(120_000)
      await page.goto(`${api.origin}/sw.js`)
      const teams = await apiFetch(page, 'GET', '/api/teams', undefined, workspaceKey)
      const team = (teams.json as unknown as { nodes: Array<{ id: string }> }).nodes[0]
      const issueResponse = await apiFetch(page, 'POST', '/api/issues', { title: 'P06 presence issue', teamId: team.id }, workspaceKey)
      expect(issueResponse.status).toBe(201)
      const issue = issueResponse.json as { id: string; identifier: string; title: string }
      const documentResponse = await apiFetch(page, 'POST', '/api/documents', { title: 'P06 presence document', content: 'Shared presence' }, workspaceKey)
      expect(documentResponse.status).toBe(201)
      const document = documentResponse.json as { id: string; slugId: string }
      const visitorContext = await browser.newContext({ viewport })
      try {
        const visitor = await signUp(visitorContext, api.origin, 'p06-visitor')
        const visitorPage = await visitorContext.newPage()
        const leaveResource = async () => {
          if (viewport.width < 600) await visitorPage.getByRole('button', { name: 'Open sidebar', exact: true }).first().click()
          await visitorPage.getByRole('link', { name: 'My issues', exact: true }).click()
        }
        await visitorPage.goto(`${api.origin}/sw.js`)
        const invited = await apiFetch(page, 'POST', `/api/workspaces/${workspaceKey}/invitations`, { emails: [visitor.email], role: 'member', teamIds: [team.id] })
        expect(invited.status).toBe(201)
        const token = (invited.json as unknown as Array<{ token: string }>)[0].token
        expect((await apiFetch(visitorPage, 'POST', '/api/invitations/accept', { token })).status).toBe(200)
        await observePresence(page)
        await observePresence(visitorPage)
        const heartbeats: number[] = []
        let ownerClientId = ''
        let visitorClientId = ''
        for (const watched of [page, visitorPage]) watched.on('response', response => {
          if (new URL(response.url()).pathname === '/api/realtime/presence' && response.request().method() === 'POST' && response.request().headers()['x-flow-presence'] === 'minimal') {
            heartbeats.push(response.status())
            if (watched === visitorPage) visitorClientId = (response.request().postDataJSON() as { clientId: string }).clientId
            else ownerClientId = (response.request().postDataJSON() as { clientId: string }).clientId
          }
        })
        const capture = async (state: string) => {
          const directory = process.env.SCALE_ACTIVATE_B1_SHOTS ?? '/tmp/codex-scale-p06'
          if (test.info().repeatEachIndex === 0) await page.screenshot({ path: `${directory}/r2-clean-${viewport.width}-${state}.png` })
        }
        const issueURL = `${api.origin}${issuePath(workspaceKey, issue)}`
        await page.goto(issueURL)
        await expect(page.getByRole('textbox', { name: 'Issue title', exact: true })).toHaveText(issue.title)
        await expect.poll(async () => (await presenceSnapshot(page)).roster.some(item => item.clientId === ownerClientId && item.issueId === issue.id), { timeout: 25_000 }).toBe(true)
        await visitorPage.goto(issueURL)
        await expect(visitorPage.getByRole('textbox', { name: 'Issue title', exact: true })).toHaveText(issue.title)
        await expect.poll(async () => (await presenceSnapshot(visitorPage)).roster.some(item => item.clientId === visitorClientId && item.issueId === issue.id), { timeout: 25_000 }).toBe(true)
        await expect.poll(async () => (await presenceSnapshot(page)).roster.some(item => item.clientId === visitorClientId && item.issueId === issue.id)).toBe(true)
        const viewers = page.locator('.issue-presence')
        await expect(viewers.locator('.issue-presence-avatar')).toHaveCount(1, { timeout: 20_000 })
        if (viewport.width < 600) await expect(viewers).toBeHidden()
        else await expect(viewers).toBeVisible()
        await expect.poll(async () => (await presenceSnapshot(page)).types).toContain('presence.updated')
        await capture('issue-viewers')
        await leaveResource()
        await expect(viewers).toHaveCount(0, { timeout: 20_000 })
        await expect.poll(async () => (await presenceSnapshot(page)).roster.some(item => item.clientId === visitorClientId && item.issueId === issue.id)).toBe(false)
        const documentURL = `${api.origin}/${workspaceKey}/document/${document.slugId}`
        await page.goto(documentURL)
        await expect(page.getByRole('textbox', { name: 'Document title' })).toHaveValue('P06 presence document')
        await visitorPage.goto(documentURL)
        const avatars = page.locator('.document-presence__avatar')
        await expect(avatars).toHaveCount(1, { timeout: 25_000 })
        if (viewport.width < 600) {
          await expect(avatars.first()).toBeHidden()
          await expect(page.locator('.document-edited__label--desktop')).toBeHidden()
          await expect(page.locator('.document-edited__label--phone')).toBeVisible()
          await expect(page.locator('.document-edited__label--phone')).toHaveCSS('padding', '0px')
          await expect(page.locator('.document-save-state')).toBeHidden()
        } else await expect(avatars.first()).toBeVisible()
        await expect.poll(async () => (await presenceSnapshot(page)).roster.some(item => item.clientId === visitorClientId && item.route === new URL(documentURL).pathname)).toBe(true)
        await capture('document-viewers')
        await leaveResource()
        await expect(avatars).toHaveCount(0, { timeout: 40_000 })
        await expect.poll(async () => (await presenceSnapshot(page)).roster.some(item => item.clientId === visitorClientId && item.route === new URL(documentURL).pathname)).toBe(false)
        await capture('document-left')
        expect(heartbeats.length).toBeGreaterThanOrEqual(4)
        expect(heartbeats.every(status => status === 204)).toBe(true)
      } finally { await visitorContext.close() }
    })
  })
}

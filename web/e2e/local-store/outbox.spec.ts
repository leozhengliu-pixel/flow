import { expect, type Page } from '@playwright/test'
import { join } from 'node:path'
import { editIssueTitle, interruptIssueWrites, readLocalRow, reloadDocument, test } from './fixture'

for (const fallback of [false, true]) {
  test.describe(fallback ? 'storage lease and storage events' : 'Web Locks and BroadcastChannel', () => {
    test.use({
      workspaceNamePrefix: fallback ? 'Outbox-fallback' : 'Outbox',
      colorScheme: fallback ? 'dark' : 'light',
      viewport: fallback ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    })

    test('hands the stream and queued edit to another tab after a lost response and reload', async ({ page, context, request, workspaceKey }, info) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const locale = fallback ? 'zh-CN' : 'en-US'
      const conflictText = fallback ? '\u4e8b\u9879\u5df2\u5728\u53e6\u4e00\u4e2a\u4f1a\u8bdd\u4e2d\u4fee\u6539' : 'Issue changed in another session'
      const editTitle = (tab: Page) => tab.getByRole('textbox', { name: fallback ? '\u4e8b\u9879\u6807\u9898' : 'Issue title', exact: true })
      expect((await request.patch('/api/account/settings', { headers, data: { language: locale, pulseSchedule: 'never', timezone: 'UTC' } })).ok()).toBe(true)
      const { teams: [team], states } = await (await request.get('/api/bootstrap', { headers })).json() as {
        teams: { id: string; key: string }[]; states: { teamId?: string; id: string; type: string }[]
      }
      const state = states.find(state => state.teamId === team.id && state.type === 'unstarted') ?? states.find(state => !state.teamId && state.type === 'unstarted')
      expect(state).toBeDefined()
      const start = 'Saved before the owner closed'
      const response = await request.post('/api/issue-records', { headers, data: { teamId: team.id, stateId: state!.id, title: start } })
      expect(response.status()).toBe(201)
      const { id, version } = await response.json() as { id: string; version: number }
      const pathname = `/${workspaceKey}/team/${team.key.toLowerCase()}/all`
      const url = `/api/issue-records/${id}`
      let applied = false
      let allowReplay = false
      let ownerRequest = false
      let conflict = false
      let rejectNext = false
      const attempts: string[] = []
      await interruptIssueWrites(context, url, (source, mutationId) => {
        attempts.push(mutationId)
        if (rejectNext) return conflict ? 'send' : 'disconnect'
        if (!applied) {
          ownerRequest = source === page
          return 'lose-response'
        }
        return allowReplay ? 'send' : 'disconnect'
      }, status => {
        applied = status === 200
        if (!applied) console.log(`First queued PATCH returned ${status}`)
      })
      await context.addInitScript(({ fallback, locale }) => {
        localStorage.setItem('flow:locale', locale)
        if (fallback) {
          Object.defineProperty(navigator, 'locks', { value: undefined })
          Object.defineProperty(window, 'BroadcastChannel', { value: undefined })
        }
        const Original = EventSource
        let count = 0
        class ObservedEventSource extends Original {
          private readonly workspaceStream: boolean
          constructor(url: string | URL, options?: EventSourceInit) {
            super(url, options)
            this.workspaceStream = new URL(url, location.href).pathname === '/api/realtime/events'
            if (this.workspaceStream) document.documentElement.dataset.fixtureStreams = String(++count)
          }
          close() {
            if (this.workspaceStream && this.readyState !== Original.CLOSED) document.documentElement.dataset.fixtureStreams = String(--count)
            super.close()
          }
        }
        window.EventSource = ObservedEventSource
      }, { fallback, locale })
      const activeStreams = (tab: Page) => tab.locator('html').getAttribute('data-fixture-streams').then(value => Number(value ?? 0))
      const open = async (tab: Page) => {
        await tab.goto(pathname, { waitUntil: 'domcontentloaded' })
        await expect(tab.getByText(start, { exact: true })).toBeVisible()
        await expect.poll(() => readLocalRow(tab, 'workspace', '_meta')).toHaveProperty('resume')
      }
      await open(page)
      const coordination = await page.evaluate(() => ({
        locks: typeof navigator.locks?.request === 'function', channel: typeof BroadcastChannel === 'function',
      }))
      expect(coordination).toEqual({ locks: !fallback, channel: !fallback })
      if (!fallback) {
        await expect.poll(() => page.evaluate(async () => (
          (await navigator.locks.query()).held?.filter(lock => lock.name.startsWith('flow-sync:')).length
        ))).toBe(1)
      }
      await expect.poll(() => page.evaluate(() => performance.getEntriesByName('flow:local-store:checksum-match').length)).toBeGreaterThan(0)
      await expect.poll(() => activeStreams(page)).toBe(1)
      let follower = await context.newPage()
      await open(follower)
      await expect.poll(async () => (await activeStreams(page)) + (await activeStreams(follower))).toBe(1)

      await follower.getByText(start, { exact: true }).click()
      let title = editTitle(follower)
      await expect(title).toHaveText(start)
      const edited = 'Retried from the other owner'
      await editIssueTitle(title, edited)
      await expect.poll(() => applied).toBe(true)
      expect(ownerRequest).toBe(true)
      const mutationId = attempts[0]
      expect(mutationId).toMatch(/^[0-9a-f-]{36}$/)
      await expect.poll(() => readLocalRow(follower, mutationId, '_outbox')).toMatchObject({ id: mutationId, input: { title: edited } })
      await expect(follower).toHaveURL(/\/retried-from-the-other-owner$/)
      const queued = 'Second edit from the same tab after the lost reply'
      await editIssueTitle(title, queued)
      const pendingCount = () => follower.evaluate(async () => {
        const { name } = (await indexedDB.databases()).find(db => db.name?.startsWith('flow-ls:')) ?? {}
        if (!name) return 0
        return new Promise<number>((resolve, reject) => {
          const open = indexedDB.open(name)
          open.onerror = () => reject(open.error)
          open.onsuccess = () => {
            const read = open.result.transaction('_outbox').objectStore('_outbox').count()
            read.onsuccess = () => resolve(read.result)
            read.onerror = () => reject(read.error)
            open.result.close()
          }
        })
      })
      await expect.poll(pendingCount).toBe(2)
      const readCheckpoint = async () => await readLocalRow(follower, 'workspace', '_meta') as unknown as {
        baseline: string; authzEpoch: string
      }
      const checkpoint = await readCheckpoint()
      await page.close()
      // Closing a tab can skip pagehide. The lease then needs to expire.
      await expect.poll(() => activeStreams(follower), { timeout: fallback ? 15_000 : 5_000 }).toBe(1)
      if (!fallback) {
        const directoryChange = await request.post('/api/labels', { headers, data: { name: 'Directory refresh during replay', color: '#676f88' } })
        expect(directoryChange.status()).toBe(201)
      }
      // Reopen from disk while writes still fail. A fresh page also avoids
      // racing the detail route's canonical URL update in the editing tab.
      const editor = follower
      follower = await context.newPage()
      title = editTitle(follower)
      await follower.goto(pathname, { waitUntil: 'commit' })
      await expect(follower.getByText(queued, { exact: true })).toBeVisible()
      if (!fallback) {
        await expect.poll(async () => (await readCheckpoint())?.baseline).not.toBe(checkpoint.baseline)
        expect((await readCheckpoint()).authzEpoch).toBe(checkpoint.authzEpoch)
        await expect.poll(pendingCount).toBe(2)
      }
      await reloadDocument(follower)
      await expect(follower.getByText(queued, { exact: true })).toBeVisible()
      await expect.poll(pendingCount).toBe(2)
      const replay = follower.waitForResponse(response => response.url().endsWith(url) && response.request().method() === 'PATCH' && response.ok())
      await editor.close()
      allowReplay = true
      await expect.poll(() => activeStreams(follower), { timeout: fallback ? 15_000 : 5_000 }).toBe(1)
      expect((await (await replay).json()).version).toBe(version + 1)
      await expect(follower.getByText(queued, { exact: true })).toBeVisible()
      await expect.poll(async () => (await (await request.get(url, { headers })).json()).version).toBe(version + 2)
      expect(new Set(attempts).size).toBe(2)
      expect(attempts).not.toContain('')
      await expect.poll(() => readLocalRow(follower, mutationId, '_outbox')).toMatchObject({ outcome: { ok: true } })
      await reloadDocument(follower)
      await expect(follower.getByText(queued, { exact: true })).toBeVisible()
      await expect(follower.locator('html')).toHaveAttribute('lang', fallback ? 'zh-CN' : 'en')
      await expect(follower.locator('html')).toHaveAttribute('data-theme', fallback ? 'dark' : 'light')
      const shots = process.env.FLOW_LOCAL_STORE_SHOTS
      const imageName = `outbox-${info.project.name}${fallback ? '-phone' : ''}`
      if (shots) await follower.screenshot({ path: join(shots, `${imageName}-failover.png`), animations: 'disabled' })

      rejectNext = true
      await follower.getByText(queued, { exact: true }).click()
      await expect(title).toHaveText(queued)
      await editIssueTitle(title, 'An offline edit based on an older title')
      await expect.poll(() => new Set(attempts).size).toBe(3)
      const latest = 'Changed in the other session'
      expect((await request.patch(url, { headers, data: { title: latest, expectedVersion: version + 2 } })).ok()).toBe(true)
      conflict = true
      await expect(title).toHaveText(latest)
      const toast = follower.getByText(conflictText, { exact: true })
      await expect(toast).toBeVisible()
      expect((await (await request.get(url, { headers })).json()).title).toBe(latest)
      if (shots) {
        await follower.screenshot({ path: join(shots, `${imageName}-conflict.png`), animations: 'disabled' })
        await toast.locator('xpath=ancestor::li[@data-sonner-toast]').screenshot({ path: join(shots, `${imageName}-toast.png`), animations: 'disabled' })
      }
      console.log(`local-store ${info.project.name} ${fallback ? 'storage fallback' : 'native owner'}: one stream, two committed edits across owner failover and reload, one surfaced 409`)
    })
  })
}

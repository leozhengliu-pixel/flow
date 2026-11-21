import { expect, type Page } from '@playwright/test'
import { join } from 'node:path'
import { readLocalRow, reloadDocument, test } from './fixture'

test.use({ workspaceNamePrefix: 'Partials' })

function measureDetailOpen(page: Page, id: string, comment: string) {
  return page.evaluate(({ id, comment }) => {
    const onClick = (event: MouseEvent) => {
      const hit = (event.target as Element).closest('[data-issue-row-id]')
      if (hit?.getAttribute('data-issue-row-id') !== id) return
      document.removeEventListener('click', onClick, true)
      performance.mark('fixture:detail-click')
      const visible = new IntersectionObserver(entries => {
        const first = entries.find(entry => entry.isIntersecting && entry.intersectionRatio > 0)
        if (!first) return
        performance.measure('fixture:detail-open', { start: 'fixture:detail-click', end: first.time })
        visible.disconnect()
        clearTimeout(timeout)
      })
      const tree = new MutationObserver(() => {
        const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
        while (walker.nextNode()) {
          const node = walker.currentNode
          if (node.textContent === comment && node.parentElement) {
            visible.observe(node.parentElement)
            tree.disconnect()
            return
          }
        }
      })
      const timeout = setTimeout(() => { tree.disconnect(); visible.disconnect() }, 10_000)
      tree.observe(document.body, { subtree: true, childList: true, characterData: true })
    }
    document.addEventListener('click', onClick, true)
  }, { id, comment })
}

test('reopens authorized history and archive pages from disk and drops stale slices', async ({ page: initialPage, context, request, workspaceKey }, info) => {
  let page = initialPage
  const headers = { 'X-Workspace-Key': workspaceKey }
  expect((await request.patch('/api/account/settings', { headers, data: { pulseSchedule: 'never', language: 'en-US', timezone: 'UTC' } })).ok()).toBe(true)
  const { teams, states } = await (await request.get('/api/bootstrap', { headers })).json() as {
    teams: { id: string; key: string }[]; states: { id: string; teamId?: string; type: string }[]
  }
  const team = teams[0]
  const state = states.find(state => state.teamId === team.id && state.type === 'unstarted') ?? states.find(state => !state.teamId && state.type === 'unstarted')
  expect(state).toBeDefined()
  const prefix = `/${workspaceKey}/team/${team.key.toLowerCase()}`
  const title = 'Issue with retained activity'
  const commentText = 'Saved in the authorized history slice'
  const seed = async (title: string, archived = false) => {
    const response = await request.post('/api/issues', { headers, data: { teamId: team.id, stateId: state!.id, title, description: 'Description loaded separately' } })
    expect(response.status()).toBe(201)
    const issue = await response.json()
    if (archived) expect((await request.patch(`/api/issues/${issue.id}`, { headers, data: { archived: true } })).ok()).toBe(true)
    return issue
  }
  const { id } = await seed(title)
  const comment = await request.post(`/api/issues/${id}/comments`, { headers, data: { body: commentText } })
  expect(comment.status()).toBe(201)
  const { id: commentId } = await comment.json()
  const oldest = await seed('Oldest archived fixture', true)
  for (let i = 1; i <= 49; i++) await seed(`Archived fixture ${i}`, true)
  const archived = await seed('Newest archived fixture', true)
  await context.addInitScript(() => {
    const put = IDBObjectStore.prototype.put
    IDBObjectStore.prototype.put = function (...args: Parameters<typeof put>) {
      if (this.name === '_partial_issue' && Number(sessionStorage.getItem('fixture:quota-failures')) > 0) {
        const remaining = Number(sessionStorage.getItem('fixture:quota-failures')) - 1
        sessionStorage.setItem('fixture:quota-failures', String(remaining))
        sessionStorage.setItem('fixture:quota-retry', 'true')
        throw new DOMException('Fixture quota exceeded', 'QuotaExceededError')
      }
      return put.apply(this, args)
    }
  })
  const dataRequests: string[] = []
  const trackRequests = (tab: Page) => tab.on('request', request => dataRequests.push(new URL(request.url()).pathname))
  const go = async (route: string, quotaFailures?: number) => {
    if (page.url() !== 'about:blank') {
      // Carry the origin's database into the next visit without racing the
      // preceding document's reload or in-app URL canonicalization.
      await page.close()
      page = await context.newPage()
      trackRequests(page)
    }
    if (quotaFailures) await page.addInitScript(count => {
      if (sessionStorage.getItem('fixture:quota-failures') === null) {
        sessionStorage.setItem('fixture:quota-failures', String(count))
      }
    }, quotaFailures)
    await page.goto(`${prefix}/${route}`, { waitUntil: 'commit' })
  }
  const manifestLog: unknown[] = []
  page.on('response', response => {
    if (!response.url().includes('/api/sync/manifest?')) return
    const status = response.status()
    void response.json().then(({ head, eventCursor, revision }) => { manifestLog.push({ status, head, eventCursor, revision }) }, () => { manifestLog.push({ status }) })
  })
  await go('all')
  const issueRow = () => page.locator(`[data-issue-row-id="${id}"]`).getByText(title, { exact: true })
  await expect(issueRow()).toBeVisible()
  await expect.poll(() => readLocalRow(page, 'workspace', '_meta')).toMatchObject({ resume: { schema: 2 } })
  await expect.poll(() => readLocalRow(page, id)).toMatchObject({ title })
  await issueRow().click()
  await expect(page.getByRole('textbox', { name: 'Issue title', exact: true })).toHaveText(title)
  await expect(page.getByText(commentText, { exact: true })).toBeVisible()
  const historyKey = `history:issue:${id}`
  await expect.poll(() => readLocalRow(page, historyKey, '_partial_activity')).toMatchObject({ key: historyKey, complete: true }).catch(async error => {
    const meta = await readLocalRow(page, 'workspace', '_meta') as unknown as { head: string; resume?: { eventCursor: string; revision: string }; sliceBytes?: number }
    console.log('history retention fixture', { head: meta?.head, resume: meta?.resume, bytes: meta?.sliceBytes, manifestLog })
    throw error
  })

  await page.goBack({ waitUntil: 'commit' })
  await expect(issueRow()).toBeVisible()
  await reloadDocument(page)
  await expect(issueRow()).toBeVisible()
  await expect.poll(() => page.evaluate(() => performance.getEntriesByName('flow:local-store:resume').length)).toBeGreaterThan(0)
  trackRequests(page)
  await measureDetailOpen(page, id, commentText)
  await issueRow().click()
  await expect(page.getByText(commentText, { exact: true })).toBeVisible()
  const duration = () => page.evaluate(() => performance.getEntriesByName('fixture:detail-open').at(-1)?.duration)
  await expect.poll(duration).toBeGreaterThan(0)
  const elapsed = (await duration())!
  console.log(`local-store ${info.project.name}: first detail with retained history ${elapsed.toFixed(1)} ms`)
  if (elapsed >= 150) {
    const timings = await page.evaluate(() => {
      const click = performance.getEntriesByName('fixture:detail-click').at(-1)!.startTime
      return {
        requests: (performance.getEntriesByType('resource') as PerformanceResourceTiming[])
          .filter(entry => entry.startTime < click + 600 && entry.responseEnd >= click - 50)
          .map(entry => [new URL(entry.name).pathname, Math.round(entry.startTime - click), Math.round(entry.responseEnd - click)]),
        phases: performance.getEntriesByType('mark').filter(entry => entry.startTime >= click - 20 && entry.name.startsWith('flow:local-store:detail-'))
          .map(entry => [entry.name, Math.round(entry.startTime - click)]),
      }
    })
    console.log('detail timings relative to click (ms)', timings)
  }
  expect(dataRequests).not.toContain(`/api/issue-records/${id}/history`)
  expect(dataRequests).not.toContain('/api/bootstrap')
  expect(dataRequests).not.toContain('/api/issue-records/bootstrap')
  const shots = process.env.FLOW_LOCAL_STORE_SHOTS
  if (shots) await page.screenshot({ path: join(shots, `partials-${info.project.name}-history.png`), animations: 'disabled' })

  await go('archive/issues', 1)
  await expect(page.getByText(`${archived.identifier} ${archived.title}`, { exact: true })).toBeVisible()
  const archiveKey = `archive:team:${team.id}:50:`
  await expect.poll(() => readLocalRow(page, archiveKey, '_partial_issue')).toMatchObject({ key: archiveKey, complete: false, cursor: expect.any(String) })
  expect(await page.evaluate(() => sessionStorage.getItem('fixture:quota-retry'))).toBe('true')
  await expect.poll(() => readLocalRow(page, historyKey, '_partial_activity')).toBeUndefined()
  expect(await readLocalRow(page, id)).toMatchObject({ title })
  const archiveList = page.locator('.archive-list')
  const scrollEnd = () => archiveList.evaluate(node => { node.scrollTop = node.scrollHeight })
  await scrollEnd()
  await expect(page.getByText(`${oldest.identifier} ${oldest.title}`, { exact: true })).toBeVisible()
  const { cursor } = await readLocalRow(page, archiveKey, '_partial_issue') as unknown as { cursor: string }
  const nextKey = `${archiveKey}${cursor}`
  await expect.poll(() => readLocalRow(page, nextKey, '_partial_issue')).toMatchObject({ key: nextKey, complete: true })
  dataRequests.length = 0
  await reloadDocument(page)
  await expect(page.getByText(`${archived.identifier} ${archived.title}`, { exact: true })).toBeVisible()
  await scrollEnd()
  await expect(page.getByText(`${oldest.identifier} ${oldest.title}`, { exact: true })).toBeVisible()
  expect(dataRequests).not.toContain('/api/issue-records')
  if (shots) await page.screenshot({ path: join(shots, `partials-${info.project.name}-archive.png`), animations: 'disabled' })

  await go('all')
  await expect(issueRow()).toBeVisible()
  await issueRow().click()
  await expect(page.getByText(commentText, { exact: true })).toBeVisible()
  await expect.poll(() => readLocalRow(page, historyKey, '_partial_activity')).toMatchObject({ key: historyKey, complete: true })
  await page.goBack({ waitUntil: 'commit' })
  await expect(issueRow()).toBeVisible()
  dataRequests.length = 0
  const updated = 'History changed while the detail was closed'
  expect((await request.patch(`/api/issues/${id}/comments/${commentId}`, { headers, data: { body: updated } })).ok()).toBe(true)
  await issueRow().click()
  await expect(page.getByText(updated, { exact: true })).toBeVisible()
  await expect(page.getByText(commentText, { exact: true })).toHaveCount(0)
  expect(dataRequests).toContain(`/api/issue-records/${id}/history`)

  // The latest change invalidates the archived slices too. A persistent quota
  // failure must still return the network page and remove the usable baseline.
  await go('archive/issues', 2)
  await expect(page.getByText(`${archived.identifier} ${archived.title}`, { exact: true })).toBeVisible()
  await expect.poll(() => readLocalRow(page, id)).toBeUndefined()
  expect(await page.evaluate(() => sessionStorage.getItem('fixture:quota-failures'))).toBe('0')
  if (process.env.FLOW_LOCAL_STORE_WEB_DIST) expect(elapsed).toBeLessThan(150)
})

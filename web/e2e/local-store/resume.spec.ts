import { expect, type Page, type Response } from '@playwright/test'
import { join } from 'node:path'
import { editIssueTitle, readLocalRow, reloadDocument, test } from './fixture'
import { observeIssueListPaint } from './timing'

test.use({ workspaceNamePrefix: 'Resume' })

const workspaceBootstrap = (response: Response) => ['/api/bootstrap', '/api/issue-records/bootstrap'].includes(new URL(response.url()).pathname)
const journal = (response: Response) => new URL(response.url()).pathname === '/api/sync/changes' && response.url().includes('mode=issues')

function startupBytes(page: Page) {
  return page.evaluate(() => {
    const resources = performance.getEntriesByType('resource') as PerformanceResourceTiming[]
    const relevant = resources.filter(entry => {
      const url = new URL(entry.name)
      return url.pathname === '/api/bootstrap' || url.pathname === '/api/issue-records/bootstrap' ||
        url.pathname.startsWith('/api/sync/') && url.searchParams.get('surface') === 'local-store'
    })
    return relevant.reduce((totals, entry) => ({
      transfer: totals.transfer + entry.transferSize,
      encoded: totals.encoded + entry.encodedBodySize,
      decoded: totals.decoded + entry.decodedBodySize,
    }), { transfer: 0, encoded: 0, decoded: 0 })
  })
}

test('resumes a retained workspace with a byte-bounded catch-up and resets an expired cursor', async ({ page, context, request, workspaceKey }, info) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const shots = process.env.FLOW_LOCAL_STORE_SHOTS
  const realAuth = process.env.FLOW_LOCAL_STORE_REAL_AUTH === '1'
  if (realAuth) await context.addCookies((await request.storageState()).cookies)
  expect((await request.patch('/api/account/settings', { headers, data: { pulseSchedule: 'never', timezone: 'UTC' } })).status()).toBe(200)
  const baseline = await request.get('/api/bootstrap', { headers })
  const { members, teamMembers, teams, viewer } = await baseline.json()
  const team = teams[0]
  const labelResponse = await request.post('/api/labels', { headers, data: { name: 'Resume label', color: '#676f88' } })
  expect(labelResponse.status()).toBe(201)
  const label = await labelResponse.json()
  const description = 'The description is read by the issue detail loader.'
  const seed = await request.post('/api/issues', { headers, data: { teamId: team.id, title: 'Retained issue', description, labelIds: [label.id] } })
  expect(seed.status()).toBe(201)
  const { id } = await seed.json()
  if (shots) {
    if (realAuth) {
      const profile = await request.patch('/api/account/profile', { headers, data: { displayName: '\u590f \u96e8', username: viewer.username || 'storage-tester' } })
      expect(profile.ok()).toBe(true)
    }
    const reviewLabel = await request.post('/api/labels', { headers, data: { name: '复核', color: '#676f88' } })
    expect(reviewLabel.status()).toBe(201)
    const { id: reviewLabelId } = await reviewLabel.json()
    for (const title of ['核对', 'Review the translated notification labels and shared workspace preferences before the next release']) {
      expect((await request.post('/api/issues', { headers, data: { teamId: team.id, title, assigneeId: viewer.id, labelIds: [reviewLabelId] } })).status()).toBe(201)
    }
  }
  for (let index = 0; index < 64; index++) {
    const issue = await request.post('/api/issues', { headers, data: { teamId: team.id, title: `Repeat visit fixture ${index}`, description } })
    expect(issue.status()).toBe(201)
  }
  const path = `/${workspaceKey}/team/${team.key.toLowerCase()}/all`
  await context.addInitScript(() => performance.setResourceTimingBufferSize(2000))
  await context.addInitScript(() => {
    if (!localStorage.getItem('flow:locale')) localStorage.setItem('flow:locale', 'en-US')
  })
  await context.addInitScript(observeIssueListPaint, Boolean(process.env.FLOW_LOCAL_STORE_PROFILE))
  await page.emulateMedia({ colorScheme: 'light' })
  await page.setViewportSize({ width: 1440, height: 900 })
  const coldBootstraps: string[] = []
  const recordColdBootstrap = (response: Response) => {
    if (workspaceBootstrap(response)) coldBootstraps.push(new URL(response.url()).pathname)
  }
  page.on('response', recordColdBootstrap)
  await page.goto(path)
  await expect(page.getByText('Retained issue', { exact: true })).toBeVisible()
  await expect.poll(() => readLocalRow(page, id)).toMatchObject({ title: 'Retained issue', labels: [expect.objectContaining({ id: label.id })] })
  page.off('response', recordColdBootstrap)
  expect(coldBootstraps).toEqual(['/api/issue-records/bootstrap'])
  expect(await readLocalRow(page, 'workspace', '_meta')).toHaveProperty('resume')
  expect(await readLocalRow(page, 'directory', '_meta')).toMatchObject({
    members: members.map(({ lastSeenAt: _lastSeenAt, ...member }: { lastSeenAt?: string }) => member),
    teamMembers,
  })
  expect(await readLocalRow(page, id)).not.toHaveProperty('description')
  await page.evaluate(async headers => {
    performance.clearResourceTimings()
    const response = await fetch('/api/bootstrap', { headers, cache: 'no-store' })
    if (!response.ok || response.headers.get('Content-Encoding') !== 'gzip') throw new Error('Expected a compressed legacy bootstrap')
    await response.arrayBuffer()
  }, headers)
  const legacy = await startupBytes(page)
  expect(legacy.transfer).toBeGreaterThan(legacy.encoded)
  expect(legacy.encoded).toBeLessThan(legacy.decoded)

  // Let two versions commit while there is no active tab to update the disk.
  await page.close()
  for (const title of ['Offline first edit', 'Offline latest edit']) {
    expect((await request.patch(`/api/issues/${id}`, { headers, data: { title } })).status()).toBe(200)
  }
  const navigate = async (warm: Page, reload = false) => {
    const responses: Response[] = []
    const collect = (response: Response) => responses.push(response)
    warm.on('response', collect)
    const firstCatchup = warm.waitForResponse(journal)
    const document = reload && process.env.FLOW_LOCAL_STORE_PROFILE
      ? warm.waitForResponse(response => response.request().isNavigationRequest()) : undefined
    if (reload) await reloadDocument(warm)
    else await warm.goto(path, { waitUntil: 'commit' })
    expect((await firstCatchup).ok()).toBe(true)
    await expect(warm.getByText('Offline latest edit', { exact: true })).toBeVisible()
    await expect.poll(() => warm.evaluate(() => performance.getEntriesByName('flow:local-store:resume').length)).toBe(1)
    expect(await warm.evaluate(() => performance.getEntriesByName('flow:local-store:resync').length)).toBe(0)
    expect(responses.filter(workspaceBootstrap)).toHaveLength(0)
    warm.off('response', collect)
    return document ? { cachedShell: Boolean((await document).headers()['x-flow-shell-assets']) } : undefined
  }
  const visit = async (width = 1440, height = 900, dark = false) => {
    const warm = await context.newPage()
    await warm.setViewportSize({ width, height })
    await warm.emulateMedia({ colorScheme: dark ? 'dark' : 'light' })
    await navigate(warm)
    return warm
  }

  const changed = await visit()
  await expect.poll(() => readLocalRow(changed, id)).toMatchObject({ title: 'Offline latest edit', labels: [expect.objectContaining({ id: label.id })] })
  const production = !!process.env.FLOW_LOCAL_STORE_WEB_DIST
  if (production) {
    await expect.poll(() => changed.evaluate(async () => {
      const worker = navigator.serviceWorker.controller
      return worker?.state === 'activated' && !!await (await caches.open('flow-shell-v2')).match('/__flow-app-shell')
    })).toBe(true)
  }
  const edited = await startupBytes(changed)
  const samples: (typeof edited)[] = []
  const content: number[] = []
  for (let sample = 0; sample < 12; sample++) {
    await changed.waitForLoadState('load')
    await changed.evaluate(() => document.fonts.ready.then(() => undefined))
    const document = await navigate(changed, true)
    expect(await changed.evaluate(() => (performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming).type)).toBe('reload')
    if (production) {
      expect(await changed.evaluate(() => navigator.serviceWorker.controller?.state)).toBe('activated')
    }
    samples.push(await startupBytes(changed))
    const firstPaint = () => changed.evaluate(() => performance.getEntriesByName('flow:test:issue-content')[0]?.startTime)
    await expect.poll(firstPaint).toBeGreaterThan(0)
    content.push((await firstPaint())!)
    if (document) console.log({ ...document, content: content.at(-1), response: await changed.evaluate(() => (performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming).responseStart) })
    if (sample === 4 && process.env.FLOW_LOCAL_STORE_PROFILE) {
      console.log(await changed.evaluate(() => {
        const nav = performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming
        const paint = performance.getEntriesByName('flow:test:issue-content')[0].startTime
        const resources = (performance.getEntriesByType('resource') as PerformanceResourceTiming[])
          .filter(entry => entry.startTime < paint)
        return {
          nav: [nav.workerStart, nav.responseStart, nav.responseEnd, nav.domInteractive], paint,
          marks: performance.getEntriesByType('mark').map(entry => [entry.name, Math.round(entry.startTime)]),
          resources: resources.filter(entry => !entry.name.includes('/assets/') || /(?:app-entry|index|issue-explorer|startup)-/.test(entry.name) || entry.responseEnd > paint - 100)
            .map(entry => [new URL(entry.name).pathname, Math.round(entry.startTime), Math.round(entry.responseEnd)]),
          assets: resources.filter(entry => entry.name.includes('/assets/')).length,
        }
      }))
    }
  }
  await expect.poll(() => changed.evaluate(() => {
    const list = document.querySelector<HTMLElement>('[role="list"][aria-label="Issues"]')
    const probe = document.querySelector<HTMLElement>('[data-observe-width]')
    if (!list || !probe) return false
    const width = Math.floor(probe.getBoundingClientRect().width)
    return width > 0 && list.style.getPropertyValue('--issue-identifier-width') === `${width}px`
  })).toBe(true)
  await changed.close()
  const p75 = content.sort((a, b) => a - b)[Math.ceil(content.length * .75) - 1]
  console.log(`local-store ${info.project.name}: first visible issue p75 ${p75.toFixed(1)} ms (${content.map(value => value.toFixed(1)).join(', ')})`)
  const worst = samples.reduce((a, b) => a.transfer >= b.transfer ? a : b)
  const meanTransfer = [edited, ...samples].reduce((sum, sample) => sum + sample.transfer, 0) / (samples.length + 1)
  console.log(`local-store ${info.project.name}: gzip/decoded/transfer bytes, legacy ${JSON.stringify(legacy)}, edited ${JSON.stringify(edited)}, worst unchanged ${JSON.stringify(worst)}, mean transfer ${meanTransfer.toFixed(1)}, 0/${samples.length + 1} unforced resyncs`)
  expect(meanTransfer / legacy.transfer).toBeLessThan(0.1)
  for (const sample of [edited, ...samples]) {
    expect(sample.encoded).toBeGreaterThan(0)
  }
  expect(worst.encoded / legacy.encoded).toBeLessThan(0.1)

  if (!process.env.FLOW_LOCAL_STORE_WEB_DIST) {
    const warm = await context.newPage()
    let expired = false
    await warm.route(/\/api\/sync\/changes\?.*mode=issues/, async route => {
      const response = await route.fetch()
      if (expired) { await route.fulfill({ response }); return }
      expired = true
      await route.fulfill({ response, json: { ...await response.json(), resyncRequired: true, transactions: [] } })
    })
    await warm.goto(path, { waitUntil: 'domcontentloaded' })
    await expect.poll(() => warm.evaluate(() => performance.getEntriesByName('flow:local-store:resync').length)).toBe(1)
    expect(await warm.evaluate(() => performance.getEntriesByName('flow:local-store:erase:journal-resync', 'mark').length)).toBe(1)
    await expect(warm.getByText('Offline latest edit', { exact: true })).toBeVisible()
    await expect.poll(() => readLocalRow(warm, id)).toMatchObject({ title: 'Offline latest edit' })
    expect(await readLocalRow(warm, 'workspace', '_meta')).toHaveProperty('resume')
    await warm.close()
  }

  const edit = await visit()
  await edit.getByText('Offline latest edit', { exact: true }).click()
  const title = edit.getByRole('textbox', { name: 'Issue title', exact: true })
  await expect(title).toBeVisible()
  await expect(edit.getByText(description, { exact: true })).toBeVisible()
  const updated = 'Edited from the issue detail'
  const saved = edit.waitForResponse(response => response.request().method() === 'PATCH' &&
    [`/api/issues/${id}`, `/api/issue-records/${id}`].includes(new URL(response.url()).pathname))
  await editIssueTitle(title, updated)
  expect((await saved).status()).toBe(200)
  await expect.poll(() => readLocalRow(edit, id)).toMatchObject({ title: updated })
  await edit.goBack()
  await expect(edit.getByText(updated, { exact: true })).toBeVisible()
  await expect(edit.locator('header').filter({ hasText: shots ? /^Backlog\s*67$/ : /^Backlog\s*65$/ })).toBeVisible()
  await expect(edit.locator('.sidebar-section-teams')).toContainText(team.name)
  await reloadDocument(edit)
  await expect(edit.getByText(updated, { exact: true })).toBeVisible()
  const chat = edit.getByRole('dialog', { name: 'Flow Agent chat' })
  await edit.locator('.bottom-agent').getByRole('button', { name: 'Agent', exact: true }).click()
  await expect(chat).toBeVisible()
  await edit.keyboard.press('Escape')
  await expect(chat).toBeHidden()
  const issueRow = edit.locator(`[data-issue-row-id="${id}"]`)
  await issueRow.press('x')
  await expect(issueRow).toHaveAttribute('data-selected', 'true')
  await edit.locator('[data-my-issues-bulk-bar]').getByRole('button', { name: 'Ask Flow', exact: true }).click()
  await expect(chat).toBeVisible()
  await edit.close()
  expect((await request.patch(`/api/issues/${id}`, { headers, data: { title: 'Offline latest edit' } })).ok()).toBe(true)

  const live = await visit()
  await expect.poll(() => live.evaluate(() => performance.getEntriesByName('flow:local-store:checksum-match').length)).toBeGreaterThan(0)
  const delta = live.waitForResponse(journal)
  expect((await request.patch(`/api/issues/${id}`, { headers, data: { title: 'Committed from another client' } })).ok()).toBe(true)
  expect((await delta).ok()).toBe(true)
  await expect(live.getByText('Committed from another client', { exact: true })).toBeVisible()
  await expect.poll(() => readLocalRow(live, id)).toMatchObject({ title: 'Committed from another client' })
  expect((await request.patch(`/api/issues/${id}`, { headers, data: { title: 'Offline latest edit' } })).ok()).toBe(true)
  await expect(live.getByText('Offline latest edit', { exact: true })).toBeVisible()
  await live.close()

  if (shots) {
    for (const dark of [false, true]) {
      const language = dark ? 'zh-CN' : 'en-US'
      const theme = dark ? 'dark' : 'light'
      if (dark) {
        expect((await request.patch('/api/account/settings', { headers, data: { language, interfaceTheme: 'Dark' } })).status()).toBe(200)
        const prepare = await context.newPage()
        await prepare.goto(path, { waitUntil: 'domcontentloaded' })
        await expect(prepare.locator('html')).toHaveAttribute('lang', language)
        await expect(prepare.getByText('Offline latest edit', { exact: true })).toBeVisible()
        await prepare.close()
      }
      for (const width of [1440, info.project.name.startsWith('webkit') ? 375 : 390]) {
        const screen = await visit(width, width === 1440 ? 900 : 844, dark)
        await expect(screen.locator('html')).toHaveAttribute('lang', dark ? 'zh-CN' : 'en')
        await expect(screen.locator('html')).toHaveAttribute('data-theme', theme)
        expect(await screen.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
        await expect(screen.getByText('核对', { exact: true })).toBeVisible()
        if (realAuth) await expect(screen.locator('[data-user-avatar][data-script="cjk"][data-initials="2"]').first()).toHaveText('\u590f\u96e8')
        if (width === 1440) await expect(screen.getByText('复核', { exact: true }).first()).toBeVisible()
        await screen.screenshot({ path: join(shots, `resume-${info.project.name}-${width}-${theme}-${language}.png`), animations: 'disabled' })
        if (width === 1440 && !dark && info.project.name === 'chromium') {
          const agent = screen.locator('.bottom-agent').getByRole('button', { name: 'Agent', exact: true })
          await agent.hover()
          await expect(screen.getByRole('tooltip').filter({ hasText: 'New chat' })).toBeVisible()
          await screen.screenshot({ path: join(shots, 'resume-agent-hover.png'), animations: 'disabled' })
          await screen.keyboard.press('Tab')
          await agent.focus()
          await expect(agent).toBeFocused()
          expect(await agent.evaluate(node => node.matches(':focus-visible'))).toBe(true)
          await agent.screenshot({ path: join(shots, 'resume-agent-focus.png'), animations: 'disabled' })
          await agent.press('Enter')
          await expect(agent).toHaveAttribute('aria-expanded', 'true')
          const agentChat = screen.getByRole('dialog', { name: 'Flow Agent chat' })
          await expect(agentChat).toBeVisible()
          await expect(agentChat.getByLabel('Send a message to Flow Agent')).toBeEditable()
          await expect(agentChat.getByRole('button', { name: 'Send message', exact: true })).toBeDisabled()
          await agentChat.screenshot({ path: join(shots, 'resume-agent-open.png'), animations: 'disabled' })
          await screen.keyboard.press('Escape')
          await screen.locator(`[data-issue-row-id="${id}"]`).press('x')
          await expect(screen.locator(`[data-issue-row-id="${id}"]`)).toHaveAttribute('data-selected', 'true')
          await screen.screenshot({ path: join(shots, 'resume-selected-bulk-bar.png'), animations: 'disabled' })
        }
        await screen.close()
      }
    }
  }
})

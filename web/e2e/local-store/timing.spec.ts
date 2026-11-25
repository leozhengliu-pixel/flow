import { expect, test, type APIRequestContext, type BrowserContext } from '@playwright/test'
import { randomUUID } from 'node:crypto'
import { createFixtureWorkspace } from '../workspace-fixture'
import { readLocalRow, reloadDocument } from './fixture'
import { measureDetailOpen, observeIssueListPaint } from './timing'

const title = 'Issue with retained activity'
const activity = 'Saved in the authorized history slice'
type Sample = { list: number; detail: number }

async function seed(request: APIRequestContext, key: string) {
  const { urlKey } = await createFixtureWorkspace(request, 'Paired timing', key)
  const headers = { 'X-Workspace-Key': urlKey }
  expect((await request.patch('/api/account/settings', {
    headers, data: { pulseSchedule: 'never', language: 'en-US', timezone: 'UTC' },
  })).ok()).toBe(true)
  const { teams: [team] } = await (await request.get('/api/bootstrap', { headers })).json()
  const issue = await request.post('/api/issues', {
    headers, data: { teamId: team.id, title, description: 'Description loaded separately' },
  })
  expect(issue.status()).toBe(201)
  const { id } = await issue.json() as { id: string }
  // Keep the target within the first rendered window of the issue list.
  for (let index = 0; index < 64; index++) {
    expect((await request.post('/api/issues', {
      headers, data: { teamId: team.id, title: `Repeat visit fixture ${index}`, description: 'Description loaded separately' },
    })).status()).toBe(201)
  }
  expect((await request.post(`/api/issues/${id}/comments`, { headers, data: { body: activity } })).status()).toBe(201)
  return { id, path: `/${urlKey}/team/${team.key.toLowerCase()}/all` }
}

async function prepare(context: BrowserContext, fixture: Awaited<ReturnType<typeof seed>>, local: boolean) {
  await context.addInitScript(() => localStorage.setItem('flow:locale', 'en-US'))
  await context.addInitScript(observeIssueListPaint, false)
  const page = await context.newPage()
  await page.goto(fixture.path)
  const row = page.locator(`[data-issue-row-id="${fixture.id}"]`).getByText(title, { exact: true })
  await expect(row).toBeVisible()
  await expect.poll(() => page.evaluate(() => navigator.serviceWorker.controller?.state)).toBe('activated')
  // The first navigation can precede worker activation. Populate the shell
  // cache through a controlled navigation before taking any samples.
  await reloadDocument(page)
  await expect(row).toBeVisible()
  await expect.poll(() => page.evaluate(async () =>
    !!await (await caches.open('flow-shell-v2')).match('/__flow-app-shell'),
  )).toBe(true)
  if (local) {
    await expect.poll(() => readLocalRow(page, 'workspace', '_meta')).toMatchObject({ resume: { schema: 2 } })
  } else {
    expect(await page.evaluate(async () => (await indexedDB.databases()).some(db => db.name?.startsWith('flow-ls:')))).toBe(false)
    await expect.poll(() => page.evaluate(() => localStorage.getItem('flow:local-snapshot'))).not.toBeNull()
  }
  await row.click()
  await expect(page.getByText(activity, { exact: true })).toBeVisible()
  if (local) {
    const historyKey = `history:issue:${fixture.id}`
    await expect.poll(() => readLocalRow(page, historyKey, '_partial_activity')).toMatchObject({ key: historyKey, complete: true })
  }
  await page.goBack({ waitUntil: 'commit' })
  await expect(row).toBeVisible()

  const measure = async (): Promise<Sample> => {
    await page.waitForLoadState('load')
    await page.evaluate(() => document.fonts.ready.then(() => undefined))
    await reloadDocument(page)
    const listPaint = () => page.evaluate(() => performance.getEntriesByName('flow:test:issue-content')[0]?.startTime)
    await expect.poll(listPaint).toBeGreaterThan(0)
    const list = (await listPaint())!
    if (local) {
      await expect.poll(() => page.evaluate(() => performance.getEntriesByName('flow:local-store:resume').length)).toBe(1)
      expect(await page.evaluate(() => performance.getEntriesByName('flow:local-store:resync').length)).toBe(0)
    }
    await measureDetailOpen(page, fixture.id, activity)
    await row.click()
    await expect(page.getByText(activity, { exact: true })).toBeVisible()
    const detailPaint = () => page.evaluate(() => performance.getEntriesByName('fixture:detail-open').at(-1)?.duration)
    await expect.poll(detailPaint).toBeGreaterThan(0)
    const detail = (await detailPaint())!
    await page.goBack({ waitUntil: 'commit' })
    await expect(row).toBeVisible()
    return { list, detail }
  }
  return { page, measure }
}

function summarize(samples: number[]) {
  const ordered = [...samples].sort((a, b) => a - b)
  const mid = Math.floor(ordered.length / 2)
  return {
    median: ordered.length % 2 ? ordered[mid] : (ordered[mid - 1] + ordered[mid]) / 2,
    p75: ordered[Math.ceil(ordered.length * .75) - 1],
  }
}

test('compares repeat list and first detail timing with the store-off build', async ({ browser, baseURL }, info) => {
  test.setTimeout(180_000)
  const baselineURL = `http://127.0.0.1:${Number(process.env.FLOW_LOCAL_STORE_BASELINE_API_PORT ?? 43381)}`
  const implementations = ['storeOn', 'storeOff'] as const
  const options = { viewport: { width: 1440, height: 900 }, colorScheme: 'light', timezoneId: 'UTC' } as const
  const storeOn = await browser.newContext({ ...options, baseURL })
  const storeOff = await browser.newContext({ ...options, baseURL: baselineURL })
  try {
    const key = `browser-${randomUUID()}`
    const targets = {
      storeOn: await prepare(storeOn, await seed(storeOn.request, key), true),
      storeOff: await prepare(storeOff, await seed(storeOff.request, key), false),
    }
    const rounds: Record<typeof implementations[number], Sample[]>[] = []
    for (let index = 0; index < 5; index++) {
      const order = index % 2 ? [...implementations].reverse() : implementations
      const round: typeof rounds[number] = { storeOn: [], storeOff: [] }
      for (const build of order) {
        const target = targets[build]
        await target.page.bringToFront()
        for (let sample = 0; sample < 3; sample++) round[build].push(await target.measure())
      }
      rounds.push(round)
      console.log(`timing ${info.project.name} round ${index + 1} (${order.join(', ')}), ms: ${JSON.stringify(round)}`)
    }
    const limits = { list: 300, detail: 150 }
    for (const metric of ['list', 'detail'] as const) {
      const on = summarize(rounds.flatMap(round => round.storeOn.map(sample => sample[metric])))
      const off = summarize(rounds.flatMap(round => round.storeOff.map(sample => sample[metric])))
      const roundMedians = rounds.map(round => summarize(round.storeOn.map(sample => sample[metric])).median)
      console.log(`timing ${info.project.name} ${metric}, ms: ${JSON.stringify({ on, off, roundMedians })}`)
      expect.soft(on.median, `${metric} median compared with store-off`).toBeLessThanOrEqual(off.median * 1.1)
      expect.soft(Math.min(...roundMedians), `${metric} median in at least one paired round`).toBeLessThan(limits[metric])
    }
  } finally {
    await storeOn.close()
    await storeOff.close()
  }
})

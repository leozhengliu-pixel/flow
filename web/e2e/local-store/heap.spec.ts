import { expect, type Page } from '@playwright/test'
import { createConnection } from 'node:net'
import { join } from 'node:path'
import { readLocalRow, test } from './fixture'

test.use({ workspaceNamePrefix: 'Heap' })

async function firefoxHeap(page: Page) {
  const path = join(process.env.FLOW_LOCAL_STORE_ARTIFACTS ?? '/tmp/codex-web-local-first', 'ff.sock')
  const socket = createConnection(path).setTimeout(15_000, () => socket.destroy(new Error('Firefox memory reporter timed out')))
  const chunks = socket[Symbol.asyncIterator]()
  let buffer = Buffer.alloc(0)
  const receive = async () => {
    for (;;) {
      const colon = buffer.indexOf(58)
      if (colon >= 0) {
        const end = colon + 1 + Number(buffer.subarray(0, colon).toString())
        if (buffer.length >= end) {
          const result = JSON.parse(buffer.subarray(colon + 1, end).toString())
          buffer = buffer.subarray(end)
          return result
        }
      }
      const chunk = await chunks.next()
      if (chunk.done) throw new Error('Firefox debugger disconnected')
      buffer = Buffer.concat([buffer, chunk.value])
    }
  }
  const call = async (to: string, type: string) => {
    const packet = JSON.stringify({ to, type })
    socket.write(`${Buffer.byteLength(packet)}:${packet}`)
    for (;;) {
      const response = await receive()
      if (response.from !== to || response.type === 'garbage-collection') continue
      if (response.error) throw new Error(`Firefox ${type}: ${response.error}`)
      return response
    }
  }
  try {
    await receive()
    const { tabs } = await call('root', 'listTabs')
    const tab = tabs.find((item: { url: string }) => item.url === page.url())
    expect(tab).toBeDefined()
    const { frame } = await call(tab.actor, 'getTarget')
    await call(frame.memoryActor, 'attach')
    try {
      const census = await call(frame.memoryActor, 'takeCensus')
      const sum = (value: unknown): number => {
        if (!value || typeof value !== 'object') return 0
        if ('bytes' in value && typeof value.bytes === 'number') return value.bytes
        return Object.values(value).reduce((total, child) => total + sum(child), 0)
      }
      return sum(census)
    } finally { await call(frame.memoryActor, 'detach') }
  } finally { socket.destroy() }
}

async function heapBytes(page: Page, engine: string) {
  await page.requestGC()
  if (engine === 'firefox') return firefoxHeap(page)
  if (engine === 'webkit') {
    // Playwright's WebKit transport exposes the Inspector heap snapshot used
    // by Web Inspector. Format 3 uses [id, selfSize, class, flags] per node.
    const client = page as unknown as { _connection: { toImpl(page: Page): { delegate: { _session: {
      send(name: 'Heap.enable' | 'Heap.disable'): Promise<void>
      send(name: string): Promise<{ snapshotData: string }>
    } } } } }
    const session = client._connection.toImpl(page).delegate._session
    await session.send('Heap.enable')
    let snapshotData: string
    try {
      ({ snapshotData } = await session.send('Heap.snapshot'))
    } finally {
      // Release the inspector's snapshot chain between measurements.
      await session.send('Heap.disable').catch(() => undefined)
    }
    const { type, version, nodes, nodeClassNames } = JSON.parse(snapshotData)
    expect([type, version]).toEqual(['Inspector', 3])
    let bytes = 0
    const byClass = process.env.FLOW_LOCAL_STORE_PROFILE === '1' ? new Map<number, number>() : undefined
    for (let i = 1; i < nodes.length; i += 4) {
      bytes += nodes[i]
      if (byClass) byClass.set(nodes[i + 1], (byClass.get(nodes[i + 1]) ?? 0) + nodes[i])
    }
    if (byClass) {
      const largest = [...byClass].sort((a, b) => b[1] - a[1]).slice(0, 6)
      console.log('WebKit heap by class (B)', largest.map(([id, size]) => [nodeClassNames[id], size]))
    }
    return bytes
  }
  const session = await page.context().newCDPSession(page)
  try { return (await session.send('Runtime.getHeapUsage')).usedSize }
  finally { await session.detach() }
}

test('retains no more JavaScript heap than the legacy list at 50,000 issues', async ({ browser, request, workspaceKey }, info) => {
  test.setTimeout(300_000)
  const headers = { 'X-Workspace-Key': workspaceKey }
  const { teams } = await (await request.get('/api/bootstrap', { headers })).json()
  const team = teams[0]
  expect((await request.patch('/api/account/settings', { headers, data: { pulseSchedule: 'never', language: 'en-US', timezone: 'UTC' } })).ok()).toBe(true)
  for (let batch = 0; batch < 10; batch++) {
    const csv = ['title,description', ...Array.from({ length: 5000 }, (_, i) => `Heap issue ${batch * 5000 + i},Issue body stays outside the local summary`) ].join('\n')
    const preview = await request.post('/api/imports/preview', {
      headers, multipart: { file: { name: `heap-${batch}.csv`, mimeType: 'text/csv', buffer: Buffer.from(csv) } },
    })
    expect(preview.status()).toBe(201)
    const { id } = await preview.json()
    const applied = await request.post(`/api/imports/${id}/commit`, { headers, data: { mapping: { title: 'title', description: 'description' }, teamId: team.id } })
    expect(applied.status()).toBe(202)
    await expect.poll(async () => {
      const response = await request.get(`/api/imports/${id}`, { headers })
      const { status, imported } = await response.json()
      await response.dispose()
      return [status, imported]
    }, { timeout: 30_000 }).toEqual(['completed', 5000])
  }
  const path = `/${workspaceKey}/team/${team.key.toLowerCase()}/all`
  const measure = async (useLocal: boolean) => {
    const context = await browser.newContext({ baseURL: info.project.use.baseURL, timezoneId: 'UTC', viewport: { width: 1440, height: 900 } })
    await context.addInitScript(useLocal => {
      // Choose the fallback before the service worker handles any requests.
      // This also lets both variants assert which startup path ran.
      const nativeFetch = window.fetch.bind(window)
      window.fetch = (input, init) => {
        if (typeof input === 'string') {
          const url = new URL(input, location.href)
          if (url.pathname === '/api/bootstrap' || url.pathname === '/api/issue-records/bootstrap') {
            performance.mark('flow:test:workspace-bootstrap')
            if (!useLocal) {
              const headers = new Headers(init?.headers)
              headers.delete('X-Flow-Local-Store')
              return nativeFetch(input, { ...init, headers })
            }
          }
          if (!useLocal && url.pathname === '/api/sync/manifest' && url.searchParams.get('mode') === 'issues') {
            return Promise.resolve(new Response(null, { status: 404 }))
          }
        }
        return nativeFetch(input, init)
      }
      window.EventSource = class extends EventSource {
        constructor(url: string | URL, options?: EventSourceInit) {
          super(url, options)
          if (new URL(url, location.href).pathname === '/api/realtime/events') performance.mark('flow:test:confirmed-workspace')
        }
      }
    }, useLocal)
    let page = await context.newPage()
    const statusCodes: { path: string; status: number }[] = []
    context.on('response', response => {
      const path = new URL(response.url()).pathname
      if (['/api/bootstrap', '/api/issue-records/bootstrap', '/api/sync/manifest', '/api/sync/records'].includes(path) && !response.ok()) {
        statusCodes.push({ path, status: response.status() })
      }
    })
    try {
      const ready = async () => {
        await expect(page.locator('[data-issue-row-id]').first()).toBeVisible({ timeout: 60_000 }).catch(error => {
          console.log(`${useLocal ? 'Local' : 'Legacy'} issue list requests`, statusCodes.slice(-10))
          throw error
        })
        await expect(page.locator('header').filter({ hasText: /^Backlog\s*50000$/ })).toBeVisible()
      }
      await page.goto(path, { waitUntil: 'domcontentloaded' })
      await ready()
      if (useLocal) {
        await expect.poll(() => readLocalRow(page, 'workspace', '_meta')).toMatchObject({ resume: { issueCount: 50_000 } })
        await expect.poll(() => page.evaluate(() => performance.getEntriesByName('flow:local-store:checksum-match').length), { timeout: 60_000 }).toBeGreaterThan(0)
      }
      // Preserve origin storage and discard the first document before reading
      // the heap. A repeat visit gets the same data without a history entry.
      await page.close()
      page = await context.newPage()
      await page.goto(path, { waitUntil: 'domcontentloaded' })
      await ready()
      // Realtime starts after authorization, including when the legacy list
      // painted from a snapshot before its bootstrap finished.
      await expect.poll(() => page.evaluate(() => performance.getEntriesByName('flow:test:confirmed-workspace').length), { timeout: 60_000 }).toBeGreaterThan(0)
      const startup = await page.evaluate(() => ({
        bootstraps: performance.getEntriesByName('flow:test:workspace-bootstrap').length,
        resumes: performance.getEntriesByName('flow:local-store:resume').length,
      }))
      expect(startup.resumes).toBe(useLocal ? 1 : 0)
      if (useLocal) expect(startup.bootstraps).toBe(0)
      else expect(startup.bootstraps).toBeGreaterThan(0)
      if (process.env.FLOW_LOCAL_STORE_PROFILE === '1') console.log(`Measuring ${useLocal ? 'local' : 'legacy'} list`)
      const samples = []
      // WebKit serializes a full object graph for each sample. Repeated
      // snapshots perturb that page's heap and can exhaust the inspector.
      const sampleCount = info.project.name === 'webkit' ? 1 : 3
      for (let i = 0; i < sampleCount; i++) samples.push(await heapBytes(page, info.project.name))
      if (useLocal) {
        await page.locator('[data-issue-row-id]').first().click()
        await expect(page.getByRole('textbox', { name: 'Issue title', exact: true })).toHaveText(/^Heap issue \d+$/)
      }
      return samples.sort((a, b) => a - b)[Math.floor(sampleCount / 2)]
    } finally { await context.close() }
  }
  // The legacy response can exceed the bounded server's read budget. Reuse
  // a recorded measurement from the same browser and fixture in that mode.
  const reference = process.env.FLOW_LOCAL_STORE_HEAP_REFERENCE
  const legacy = reference ? Number(JSON.parse(reference)[info.project.name]) : await measure(false)
  expect(legacy).toBeGreaterThan(0)
  if (reference) console.log(`local-store ${info.project.name}: using recorded legacy heap (${legacy} B)`)
  const local = await measure(true)
  console.log(`local-store ${info.project.name}: 50,000 issues, retained JS heap ${legacy} B legacy, ${local} B local`)
  expect(local).toBeGreaterThan(0)
  expect(local).toBeLessThanOrEqual(legacy)
})

import { expect, type APIRequestContext, type Page } from '@playwright/test'
import { mkdirSync, writeFileSync } from 'node:fs'
import { test } from './scale-p19-fixture'
import { scaleP19CheckRetention, scaleP19CollectHeap, scaleP19HeapOwners, scaleP19HeapSnapshot, scaleP19HeapTrend, scaleP19RetentionProbe } from './scale-p19-heap'

const root = process.env.FLOW_SCALE_P19_ARTIFACTS ?? '/tmp/codex-scale-p19'

async function directory(page: Page, request: APIRequestContext, workspace: string) {
  const headers = { 'X-Workspace-Key': workspace }
  const timeZone = await page.evaluate(() => Intl.DateTimeFormat().resolvedOptions().timeZone)
  expect((await request.patch('/api/account/settings', { headers, data: { timezone: timeZone } })).ok()).toBe(true)
  const source = await (await request.get('/api/bootstrap', { headers })).json()
  const changes = await (await request.get('/api/realtime/changes', { headers })).json()
  const head = String(changes.head)
  for (const field of ['users', 'teams', 'labels', 'labelGroups', 'projectStatuses', 'projectTemplates', 'projectRelations', 'initiatives', 'savedViews', 'favorites', 'favoriteFolders', 'subscriptions', 'agentSkills', 'invitations', 'customers', 'customerStatuses', 'customerTiers', 'customerRequests']) source[field] ??= []
  source.userSettings ??= {}
  source.teamSettings ??= {}
  source.settings ??= {}
  const status = source.projectStatuses.find((item: { type: string }) => item.type === 'started') ?? source.projectStatuses[0]
  const calls: URL[] = []
  const row = (index: number) => ({
    id: `p19-project-${index}`, name: index === 0 ? '项目' : index === 1 ? 'Long project name for the shared delivery programme across several product teams' : `Project ${String(index).padStart(5, '0')}`,
    slugId: `p19-project-${index}`, summary: 'x'.repeat(1024), description: '', icon: 'Project', color: source.teams[0].color,
    priority: 0, position: index, priorityLabel: 'No priority', progress: 0.4, health: 'onTrack', status,
    lead: { ...source.viewer, displayName: '林郑' }, memberIds: [], labelIds: [], teamIds: [source.teams[0].id], dependencyIds: [], initiatives: [], customers: [], resources: [], milestones: [], comments: [], descriptionRevisions: [], updateCadence: 'none', issueCount: 0,
    createdAt: '2026-09-15T00:00:00Z', updatedAt: '2026-09-15T00:00:00Z',
  })
  await page.route('**/api/projects?*', async route => {
    const url = new URL(route.request().url())
    calls.push(url)
    expect(route.request().headers()['x-workspace-key']).toBe(workspace)
    const offset = Number(url.searchParams.get('cursor') || 0)
    const limit = Number(url.searchParams.get('limit') || 100)
    const search = url.searchParams.get('q') ?? ''
    if (search) {
      const items = search.includes('22000') ? [row(22000)] : []
      return route.fulfill({ json: { items, total: items.length, hasMore: false } })
    }
    const filters = JSON.parse(url.searchParams.get('filter') || '[]') as Array<{ field: string; values: string[] }>
    const hidden = filters.some(filter => filter.field === 'status' && !filter.values.includes(status.name) && !filter.values.includes(status.id) || filter.field === 'priority' && !filter.values.includes('none') || filter.field === 'health' && !filter.values.includes('on-track') || filter.field === 'dates' && !filter.values.includes('no-target'))
    const items = hidden ? [] : Array.from({ length: Math.min(limit, 23210 - offset) }, (_, index) => row(offset + index))
    return route.fulfill({ json: { items, total: hidden ? 0 : 23210, hasMore: !hidden && offset + limit < 23210, nextCursor: !hidden && offset + limit < 23210 ? String(offset + limit) : undefined } })
  })
  await page.route('**/api/projects/p19-project-*/updates', route => {
    expect(route.request().headers()['x-workspace-key']).toBe(workspace)
    const projectId = new URL(route.request().url()).pathname.split('/')[3]
    return route.fulfill({ json: [{ id: `${projectId}-update`, projectId, user: { ...source.viewer, displayName: '林郑' }, body: 'Window-specific project update', health: 'onTrack', createdAt: '2026-11-12T00:00:00Z', comments: [], reactions: {} }] })
  })
  let epoch = 'p19-epoch-1'
  let capability = true
  await page.route('**/api/sync/**', route => {
    const url = new URL(route.request().url())
    if (url.pathname.endsWith('/manifest')) return route.fulfill({ json: { schema: 2, workspaceId: source.workspace.id, viewerId: source.viewer.id, authzEpoch: epoch, head, capabilities: capability ? ['sync-v2'] : [], core: { ...source, projects: [], issues: [], documents: [], projectUpdates: {} }, collections: { ...Object.fromEntries(['users', 'teams', 'labels', 'savedViews', 'favorites'].map(kind => [kind, { revision: '1', complete: true }])), projects: { revision: '1', complete: (route.request().headers()['referer'] ?? '').includes('/initiatives/') }, initiatives: { revision: '1', complete: true } } } })
    if (url.pathname.endsWith('/changes')) return route.fulfill({ json: { next: head, head, authzEpoch: epoch, transactions: [], hasMore: false, resyncRequired: false } })
    if (url.pathname.endsWith('/batch')) {
      const input = route.request().postDataJSON() as { keys: Array<{ kind: string; id: string }> }
      expect(input.keys.length).toBeLessThanOrEqual(50)
      return route.fulfill({ json: { authzEpoch: epoch, items: input.keys.map(key => ({ key, version: '1', data: row(Number(key.id.replace('p19-project-', ''))) })) } })
    }
    return route.fulfill({ status: 404, json: {} })
  })
  return { calls, revoke: () => { epoch = 'p19-epoch-2' }, oldServer: () => { capability = false } }
}

async function capture(page: Page, name: string, selector?: string) {
  await page.evaluate(() => document.fonts.ready)
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  mkdirSync(`${root}/shots`, { recursive: true })
  const target = selector ? page.locator(selector) : page
  await target.screenshot({ path: `${root}/shots/${name}.png`, animations: 'disabled', scale: 'css' })
}

for (const enabled of [false, true]) for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) for (const [theme, language] of [['Dark', 'en-US'], ['Light', 'zh-CN']]) {
  test.describe(`scale-p19 flags ${enabled} ${viewport.width} ${theme}`, () => {
    test.use({ scaleP19Enabled: enabled, viewport, isMobile: viewport.width < 500, hasTouch: viewport.width < 500 })

    test('projects and initiatives keep their layouts and explicit compatibility paths', async ({ page, request, workspaceKey }) => {
      test.setTimeout(120_000)
      const headers = { 'X-Workspace-Key': workspaceKey }
      expect((await request.patch('/api/account/settings', { headers, data: { language, interfaceTheme: theme } })).ok()).toBe(true)
      expect((await request.post('/api/initiatives', { headers, data: { name: 'P19 initiative', status: 'active' } })).status()).toBe(201)
      const fixture = await directory(page, request, workspaceKey)
      await page.goto(`/${workspaceKey}/projects/all`)
      await expect(page.getByRole('row', { name: '项目', exact: true })).toBeVisible()
      if (enabled) {
        await expect(page.locator('.scale-p19-project-pager')).toContainText('23210')
        expect(fixture.calls.some(url => url.searchParams.get('includeTotal') === 'true')).toBe(true)
        const next = page.locator('.scale-p19-project-pager button').last()
        await next.click()
        await expect(page.getByText('Project 00100', { exact: true })).toBeVisible()
        await page.locator('.scale-p19-project-pager button').first().click()
        await expect(page.getByRole('row', { name: '项目', exact: true })).toBeVisible()
      } else await expect(page.locator('.scale-p19-project-pager')).toHaveCount(0)
      await capture(page, `flags-${enabled}-projects-${viewport.width}-${theme}`)
      if (enabled) {
        const pager = page.locator('.scale-p19-project-pager')
        await expect(pager.locator('button').first()).toBeDisabled()
        await pager.locator('button').last().hover()
        await capture(page, `pager-hover-${viewport.width}-${theme}`, '.scale-p19-project-pager')
        await pager.locator('button').last().focus()
        await capture(page, `pager-focus-${viewport.width}-${theme}`, '.scale-p19-project-pager')
        await page.getByRole('row', { name: '项目', exact: true }).locator('.lp-project-row__health').click()
        await expect(page.locator('.lp-project-updates-preview')).toContainText('Window-specific project update')
        await capture(page, `updates-preview-${viewport.width}-${theme}`)
        await page.keyboard.press('Escape')
        await expect(page.locator('.lp-project-updates-preview')).toHaveCount(0)
        await page.locator('.lp-projects__new-project').click()
        const dependencies = page.locator('.lp-new-project-dependency-trigger')
        await expect(dependencies).toBeVisible()
        await dependencies.click()
        await capture(page, `dependency-open-${viewport.width}-${theme}`)
        await page.locator('.project-dependency-picker__direction').first().click()
        const search = page.locator('.project-dependency-picker__project-search input')
        await search.fill('22000')
        const result = page.locator('.project-dependency-picker__project').filter({ hasText: 'Project 22000' })
        await expect(result).toBeVisible()
        await result.hover()
        await result.focus()
        const menuBounds = (await page.locator('.project-dependency-picker__projects').boundingBox())!
        expect(menuBounds.x).toBeGreaterThanOrEqual(0)
        expect(menuBounds.x + menuBounds.width).toBeLessThanOrEqual(viewport.width)
        const preview = page.locator('.project-dependency-picker__preview')
        await expect(preview).toBeVisible()
        const previewBounds = (await preview.boundingBox())!
        expect(previewBounds.x).toBeGreaterThanOrEqual(0)
        expect(previewBounds.x + previewBounds.width).toBeLessThanOrEqual(viewport.width)
        await capture(page, `dependency-search-${viewport.width}-${theme}`)
        await result.click()
        await expect(dependencies).toContainText('Project 22000')
        await capture(page, `dependency-selected-${viewport.width}-${theme}`)
      }
      await page.goto(`/${workspaceKey}/initiatives/active`)
      await expect(page.getByText('P19 initiative', { exact: true }).first()).toBeVisible()
      await expect(page.locator('.scale-p19-project-pager')).toHaveCount(enabled ? 1 : 0)
      await capture(page, `flags-${enabled}-initiatives-${viewport.width}-${theme}`)
      if (enabled) {
        fixture.oldServer()
        await page.goto(`/${workspaceKey}/projects/all`)
        await expect(page.getByRole('row', { name: '项目', exact: true })).toBeVisible()
        await expect(page.locator('.scale-p19-project-pager')).toHaveCount(0)
        await capture(page, `old-server-projects-${viewport.width}-${theme}`)
      }
    })
    if (enabled) test('shows focus-visible and pressed pager states', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      expect((await request.patch('/api/account/settings', { headers, data: { language, interfaceTheme: theme } })).ok()).toBe(true)
      await directory(page, request, workspaceKey)
      await page.goto(`/${workspaceKey}/projects/all`)
      const pager = page.locator('.scale-p19-project-pager')
      const next = pager.locator('button').last()
      await expect(pager).toContainText('23210')
      await expect(pager.locator('button').first()).toBeDisabled()
      await page.keyboard.press('Tab')
      await next.focus()
      await expect(next).toBeFocused()
      expect(await next.evaluate(element => element.matches(':focus-visible'))).toBe(true)
      await capture(page, `pager-focus-visible-${viewport.width}-${theme}`, '.scale-p19-project-pager')
      await next.hover()
      await page.mouse.down()
      expect(await next.evaluate(element => element.matches(':active'))).toBe(true)
      await capture(page, `pager-pressed-${viewport.width}-${theme}`, '.scale-p19-project-pager')
      await page.mouse.up()
      await expect(pager).toContainText('101–200')
      await capture(page, `pager-page-selected-${viewport.width}-${theme}`, '.scale-p19-project-pager')
    })
  })
}

test.describe('scale-p19 bounded navigation and control states', () => {
  test.use({ scaleP19Enabled: true, viewport: { width: 1440, height: 900 }, isMobile: false, hasTouch: false })
  test('100 cross-page navigations retain a stable heap and preserve selected IDs', async ({ page, request, workspaceKey }, testInfo) => {
    test.setTimeout(process.env.FLOW_SCALE_P19_HEAP_SNAPSHOTS === 'true' ? 300_000 : 180_000)
    const fixture = await directory(page, request, workspaceKey)
    await page.goto(`/${workspaceKey}/projects/all`)
    await expect(page.getByRole('row', { name: '项目', exact: true })).toBeVisible()
    const pager = page.locator('.scale-p19-project-pager')
    const previous = pager.locator('button').first(), next = pager.locator('button').last()
    await expect(previous).toBeDisabled()
    await page.getByRole('row', { name: '项目', exact: true }).press('Space')
    await expect(page.getByRole('toolbar', { name: '1 selected projects' })).toBeVisible()
    await next.hover()
    await capture(page, 'pager-hover', '.scale-p19-project-pager')
    await next.focus()
    await capture(page, 'pager-focus', '.scale-p19-project-pager')
    const cdp = await page.context().newCDPSession(page)
    const probe = await scaleP19RetentionProbe(cdp)
    const initial = await probe.read()
    const samples = []
    try {
      for (let navigation = 1; navigation <= 100; navigation++) {
        await next.click()
        await expect(pager).toContainText(`${navigation * 100 + 1}–${navigation * 100 + 100}`)
        const retention = await probe.read()
        scaleP19CheckRetention(retention, navigation, initial.listeners)
        if (navigation % 20 === 0) {
          const collected = await scaleP19CollectHeap(page, cdp, 3)
          const median = [...collected.readings].sort((a, b) => a.usedSize - b.usedSize)[1]
          const sample = { navigation, ...median, ...collected, retention, diagnostic: undefined as unknown }
          if (process.env.FLOW_SCALE_P19_HEAP_SNAPSHOTS === 'true' && [20, 100].includes(navigation)) {
            await scaleP19HeapSnapshot(cdp, testInfo.outputPath(`heap-${navigation}.heapsnapshot`))
            sample.diagnostic = { owners: await scaleP19HeapOwners(cdp) }
          }
          samples.push(sample)
        }
        expect(await page.locator('.lp-project-row').count()).toBeLessThan(60)
      }
      const trend = scaleP19HeapTrend(samples)
      writeFileSync(`${root}/browser-heap.json`, JSON.stringify({ project: testInfo.project.name, samples, trend }, null, 2))
      writeFileSync(testInfo.outputPath('heap-samples.json'), JSON.stringify({ project: testInfo.project.name, samples, trend }, null, 2))
      // React DEV's sampled owner stacks vary with timing; storage bounds above remain strict on every page.
      expect(trend.lateBytesPerNavigation).toBeLessThanOrEqual(trend.allowedBytesPerNavigation)
      // A steady per-navigation leak keeps early and late slopes equal, so cap total growth as well.
      expect(samples[samples.length - 1].usedSize - samples[0].usedSize).toBeLessThanOrEqual(8 * 1024 * 1024)
    } finally {
      await probe.dispose()
    }
    await expect(page.getByRole('toolbar', { name: '1 selected projects' })).toBeVisible()
    await capture(page, 'navigation-100-projects-1440')
    fixture.revoke()
    await next.click()
    await expect(page.getByRole('row', { name: '项目', exact: true })).toBeVisible()
    // A manifest with the new epoch replaces the old page instead of installing the queued response.
    await expect(pager).toContainText('1–100')
  })
})

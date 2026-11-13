import { expect, type APIRequestContext, type Page } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import { test } from './scale-p19-fixture'

const root = process.env.FLOW_SCALE_P19_ARTIFACTS ?? '/tmp/codex-scale-p19-enable'
const display = { layout: 'list', grouping: 'No grouping', groupOrder: 'asc', subGrouping: 'No grouping', ordering: 'Manual', orderingDirection: 'asc', showClosed: 'All', showEmptyGroups: false, onlyLeadTeamProjects: false, properties: ['Priority', 'Status', 'Health', 'Lead', 'Target date', 'Issues'] }

async function seed(request: APIRequestContext, workspace: string) {
  const headers = { 'X-Workspace-Key': workspace }
  const source = await (await request.get('/api/bootstrap', { headers })).json()
  const started = source.projectStatuses.find((item: { type: string }) => item.type === 'started')
  const completed = source.projectStatuses.find((item: { type: string }) => item.type === 'completed')
  const projects = []
  for (let index = 0; index < 8; index++) {
    const name = index === 0 ? '项目' : index === 1 ? 'Long project name for the shared delivery programme across product teams' : `Project ${8 - index}`
    const response = await request.post('/api/projects', { headers, data: { name, summary: 'Visible summary', teamIds: [source.teams[0].id], statusId: index % 2 ? completed.id : started.id, priority: index % 5, startDate: '2026-01-01', targetDate: index === 7 ? '2028-12-31' : '2026-12-31', leadId: source.viewer.id } })
    expect(response.status()).toBe(201)
    projects.push(await response.json())
  }
  const initiatives = []
  for (let index = 0; index < 105; index++) {
    const response = await request.post('/api/initiatives', { headers, data: { name: `Initiative ${String(index).padStart(3, '0')}`, status: 'active', projectIds: index === 0 ? projects.map(item => item.id) : [projects[index % projects.length].id], ...(index === 1 ? { parentInitiativeIds: [initiatives[0].id] } : {}) } })
    expect(response.status()).toBe(201)
    initiatives.push(await response.json())
  }
  for (let index = 0; index < 25; index++) {
    const response = await request.post(`/api/projects/${projects[0].id}/updates`, { headers, data: { body: `Window update ${String(index).padStart(2, '0')}`, health: 'onTrack' } })
    expect(response.status()).toBe(201)
  }
  return { projects, initiatives, started, team: source.teams[0] }
}

async function setDisplay(page: Page, workspace: string, value: Partial<typeof display>) {
  await page.evaluate(({ workspace, settings }) => localStorage.setItem(`flow:projects:view:${workspace}:workspace:all`, JSON.stringify(settings)), { workspace, settings: { ...display, ...value } })
  await page.goto(`/${workspace}/projects/all`)
}
async function capture(page: Page, name: string, selector?: string) {
  await page.evaluate(() => document.fonts.ready)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  mkdirSync(`${root}/shots`, { recursive: true })
  await (selector ? page.locator(selector) : page).screenshot({ path: `${root}/shots/${name}.png`, animations: 'disabled', scale: 'css' })
}

for (const enabled of [false, true]) for (const theme of ['Light', 'Dark']) for (const viewport of [{ width: 1440, height: 900 }, { width: 375, height: 812 }, { width: 390, height: 844 }]) {
  test.describe(`scale-p19 enable actual server flags=${enabled} width=${viewport.width} theme=${theme}`, () => {
    test.use({ scaleP19Enabled: enabled, scaleP19ServerEnabled: enabled, viewport, isMobile: viewport.width < 500, hasTouch: viewport.width < 500 })
    test('preserves project, timeline, initiative, filter and selection workflows', async ({ page, request, workspaceKey }) => {
      test.setTimeout(180_000)
      const language = viewport.width < 500 ? 'zh-CN' : 'en-US'
      expect((await request.patch('/api/account/settings', { data: { language, interfaceTheme: theme } })).ok()).toBe(true)
      const fixture = await seed(request, workspaceKey)
      const requests: string[] = []
      const failures: string[] = []
      page.on('request', request => { if (new URL(request.url()).pathname.startsWith('/api/')) requests.push(request.url()) })
      page.on('pageerror', error => failures.push(error.message))
      await page.goto(`/${workspaceKey}/projects/all`)
      const first = page.getByRole('row', { name: '项目', exact: true })
      await expect(first).toBeVisible()
      if (viewport.width > 1000) await expect(page.locator('.sidebar-section-teams')).toContainText(fixture.team.name)
      if (enabled) {
        await expect(page.locator('.scale-p19-project-pager')).toContainText('/ 8')
        expect(requests.some(url => new URL(url).pathname === '/api/bootstrap')).toBe(false)
        expect((await request.patch(`/api/projects/${fixture.projects[0].id}`, { headers: { 'X-Workspace-Key': workspaceKey }, data: { name: 'Renamed 项目' } })).ok()).toBe(true)
        await expect(page.getByRole('row', { name: 'Renamed 项目', exact: true })).toBeVisible()
        expect((await request.patch(`/api/projects/${fixture.projects[0].id}`, { headers: { 'X-Workspace-Key': workspaceKey }, data: { name: '项目' } })).ok()).toBe(true)
        await expect(first).toBeVisible()
      }
      await first.press('Space')
      await expect(page.getByRole('toolbar')).toBeVisible()
      if (enabled) await capture(page, `enable-project-selection-${viewport.width}-${theme}`)

      await setDisplay(page, workspaceKey, { ordering: 'Name', grouping: 'Priority' })
      await expect(page.locator('.lp-project-row').first()).toBeVisible()
      const names = await page.locator('.lp-project-row').evaluateAll(elements => elements.map(element => element.getAttribute('aria-label')))
      expect(names).toHaveLength(8)
      await expect(page.locator('.lp-project-group__count').first()).toHaveText('2')

      await page.goto(`/${workspaceKey}/projects/all?status=${fixture.started.id}`)
      await expect(page.locator('.lp-project-row')).toHaveCount(4)
      if (enabled) await expect(page.locator('.scale-p19-project-pager')).toContainText('/ 4')

      await setDisplay(page, workspaceKey, { layout: 'timeline', ordering: 'Name', grouping: 'Status' })
      await expect(page.locator('.lp-project-timeline')).toBeVisible()
      await expect(page.locator('.lp-project-timeline__bar').first()).toBeVisible()
      if (enabled) {
        expect(requests.some(url => new URL(url).pathname === '/api/bootstrap')).toBe(false)
        await capture(page, `enable-timeline-${viewport.width}-${theme}`)
      }

      await setDisplay(page, workspaceKey, {})
      await page.getByRole('row', { name: '项目', exact: true }).locator('.lp-project-row__health').click()
      const updates = page.locator('.lp-project-updates-preview')
      await expect(updates).toContainText('Window update 24')
      if (enabled) {
        const pager = updates.locator('.scale-p19-project-pager')
        await expect(pager).toContainText('1–20 / 25')
        await expect(pager.locator('button').first()).toBeDisabled()
        await pager.locator('button').last().hover()
        await page.keyboard.press('Tab')
        await pager.locator('button').last().focus()
        expect(await pager.locator('button').last().evaluate(element => element.matches(':focus-visible'))).toBe(true)
        await capture(page, `enable-update-pager-focus-${viewport.width}-${theme}`, '.lp-project-updates-preview .scale-p19-project-pager')
        await pager.locator('button').last().click()
        await expect(pager).toContainText('21–25 / 25')
        await expect(updates).toContainText('Window update 00')
        await expect(pager.locator('button').last()).toBeDisabled()
        await capture(page, `enable-updates-${viewport.width}-${theme}`)
      }
      await page.keyboard.press('Escape')
      await page.goto(`/${workspaceKey}/initiatives/active`)
      await expect(page.locator('.li-row').first()).toContainText('Initiative 000')
      await expect(page.locator('.li-row').first()).not.toHaveClass(/is-context-only/)
      await expect(page.locator('.li-row').first().locator('.li-project-count__value')).toContainText('/ 8')
      await page.locator('.li-row').first().press('Space')
      await expect(page.getByRole('toolbar')).toBeVisible()
      if (enabled) {
        const pager = page.locator('.scale-p19-project-pager')
        await expect(pager).toContainText('/ 105')
        await pager.locator('button').last().click()
        await expect(page.locator('.li-row').first()).toContainText('Initiative 100')
        await expect(page.getByRole('toolbar')).toBeVisible()
        if (viewport.width < 500) {
          const bulk = (await page.getByRole('toolbar').boundingBox())!
          const navigation = (await pager.boundingBox())!
          expect(bulk.y + bulk.height).toBeLessThanOrEqual(navigation.y)
        }
        await capture(page, `enable-initiative-selection-${viewport.width}-${theme}`)
        await pager.locator('button').first().click()
        await expect(page.locator('.li-row').first()).toContainText('Initiative 000')
        expect(requests.some(url => new URL(url).pathname === '/api/bootstrap')).toBe(false)
      }
      expect(failures).toEqual([])
    })
  })
}

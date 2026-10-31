import { expect, test } from '@playwright/test'
import { test as workspaceTest } from './workspace-fixture'

workspaceTest.describe('mobile cycles timeline', () => {
  workspaceTest.use({ isMobile: true, hasTouch: true, colorScheme: 'dark' })

  for (const width of [390, 375]) {
    workspaceTest(`cycle cards, cooldown, chart and navigation at ${width}px`, async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Flow-Workspace': workspaceKey, 'X-Workspace-Key': workspaceKey }
      const bootstrap = await (await request.get('/api/bootstrap', { headers })).json()
      const team = bootstrap.teams[0] as { id: string; key: string }
      expect((await request.patch(`/api/teams/${team.id}/cycle-settings`, { headers, data: { enabled: true, autoCreate: true, durationWeeks: 3, cooldownWeeks: 2, upcomingCount: 1 } })).ok()).toBe(true)
      const data = await (await request.get('/api/bootstrap', { headers })).json()
      const cycles = data.cycles as { id: string; number: number; startsAt: string; endsAt: string }[]
      const first = cycles.find(cycle => cycle.number === 1)!
      const next = cycles.find(cycle => cycle.number === 2)!
      expect((await request.patch(`/api/teams/${team.id}/cycle-settings`, { headers, data: { autoCreate: false } })).ok()).toBe(true)
      expect((await request.patch(`/api/cycles/${first.id}`, { headers, data: { status: 'completed', name: '[mobile] Finished cycle' } })).ok()).toBe(true)
      expect((await request.patch(`/api/cycles/${next.id}`, { headers, data: { name: '[mobile] Next cycle' } })).ok()).toBe(true)
      expect((await request.post('/api/issues', { headers, data: { title: '[mobile] Planned work', teamId: team.id, cycleId: first.id, stateId: 'state_todo' } })).status()).toBe(201)
      await page.setViewportSize({ width, height: width === 390 ? 844 : 667 })
      await page.goto(`/${workspaceKey}/team/${team.key}/cycles`)
      const completed = page.getByTestId('cycle-timeline-1')
      const upcoming = page.getByTestId('cycle-timeline-2')
      await expect(completed).toBeVisible()
      expect((await upcoming.boundingBox())?.y).toBe(44)
      expect((await upcoming.boundingBox())?.height).toBe(70)
      expect((await completed.boundingBox())?.y).toBe(164)
      expect((await completed.boundingBox())?.height).toBe(419)
      await expect(page.locator('.flow-cycle-cooldown')).toContainText('2 weeks cooldown')
      await expect(completed.locator('.is-scope strong')).toHaveText('1')
      await expect(completed.locator('.cycle-timeline-metric-label')).toHaveText(['Scope', 'Started', 'Completed'])
      await expect(completed.locator('.estimate-icon')).toHaveCount(0)
      const chart = completed.getByRole('img')
      expect((await chart.boundingBox())?.width).toBe(width - 112)
      await expect.poll(() => chart.evaluate(node => {
        const canvas = node as HTMLCanvasElement
        return canvas.getContext('2d')!.getImageData(0, 0, canvas.width, canvas.height).data.some((value, index) => index % 4 === 3 && value > 0)
      })).toBe(true)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await page.getByRole('switch', { name: 'Add to favorites', exact: true }).click()
      await expect(page.getByRole('switch', { name: 'Remove from favorites', exact: true })).toHaveAttribute('aria-checked', 'true')
      await upcoming.click()
      await expect(page).toHaveURL(/\/cycle\/2(?:\/|$)/)
      await page.goBack()
      await page.getByTestId('cycle-timeline-1').click()
      await expect(page).toHaveURL(/\/cycle\/1(?:\/|$)/)
    })
  }
})

workspaceTest.describe('mobile cycle detail', () => {
  workspaceTest.use({ isMobile: true, hasTouch: true, colorScheme: 'dark' })

  for (const width of [390, 375]) {
    workspaceTest(`grouped estimate rows match the phone chrome at ${width}px`, async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Flow-Workspace': workspaceKey, 'X-Workspace-Key': workspaceKey }
      const bootstrap = await (await request.get('/api/bootstrap', { headers })).json()
      const team = bootstrap.teams[0] as { id: string; key: string }
      expect((await request.patch(`/api/teams/${team.id}/settings`, { headers, data: { estimateType: 'fibonacci', estimateAllowZero: true } })).ok()).toBe(true)
      expect((await request.patch(`/api/teams/${team.id}/cycle-settings`, { headers, data: { enabled: true, autoCreate: true, durationWeeks: 3, cooldownWeeks: 2, upcomingCount: 1 } })).ok()).toBe(true)
      const cycles = (await (await request.get('/api/bootstrap', { headers })).json()).cycles as { id: string; number: number }[]
      const cycle = cycles.find(item => item.number === 1)!
      const create = async (title: string, input: Record<string, unknown> = {}) => {
        const response = await request.post('/api/issues', { headers, data: { title, teamId: team.id, cycleId: cycle.id, stateId: 'state_todo', ...input } })
        expect(response.status()).toBe(201)
        return await response.json() as { id: string }
      }
      const parent = await create('Estimate test A', { estimate: 3, sortOrder: 1 })
      const second = await create('Estimate test B', { estimate: 5, sortOrder: 0 })
      await create('Nested child one', { parentId: parent.id, sortOrder: 0 })
      expect((await request.patch(`/api/issues/${second.id}`, { headers, data: { sortOrder: 0 } })).ok()).toBe(true)
      await page.setViewportSize({ width, height: width === 390 ? 844 : 667 })
      await page.goto(`/${workspaceKey}/team/${team.key}/cycle/1`)
      await page.getByRole('button', { name: 'Display options', exact: true }).click()
      await page.locator('input[aria-label="Nested sub-issues"]').click()
      await page.keyboard.press('Escape')
      await expect(page.locator('[data-cycle-detail-header]')).toHaveCSS('height', '87.5px')
      expect((await page.locator('[data-content-view-subheader]').boundingBox())?.y).toBe(44)
      expect((await page.locator('[data-my-issues-group-header]').boundingBox())?.x).toBe(8)
      await expect(page.locator('a[data-issue-row-id]').nth(0)).toContainText('Estimate test B')
      await expect(page.locator('a[data-issue-row-id]').nth(1)).toContainText('Estimate test A')
      await expect(page.locator('a[data-issue-row-id]').nth(2)).toContainText('Nested child one')
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      expect(second.id).toBeTruthy()
    })
  }
})

workspaceTest.describe('mobile initiative overview', () => {
  workspaceTest.use({ isMobile: true, hasTouch: true, colorScheme: 'dark' })

  for (const width of [390, 375]) {
    workspaceTest(`closed details preserve the overview and latest update at ${width}px`, async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Flow-Workspace': workspaceKey, 'X-Workspace-Key': workspaceKey }
      const created = await request.post('/api/initiatives', { headers, data: { name: '[mobile] Overview', status: 'active', color: '#e9b0a7' } })
      expect(created.status()).toBe(201)
      const initiative = await created.json() as { id: string; slugId: string }
      const update = await request.post(`/api/initiatives/${initiative.id}/updates`, { headers, data: { body: '[mobile] On schedule.', health: 'onTrack' } })
      expect(update.status()).toBe(201)
      await page.setViewportSize({ width, height: width === 390 ? 844 : 667 })
      await page.goto(`/${workspaceKey}/initiative/${initiative.slugId}/overview`)
      await expect(page.getByTestId('initiative-detail-header')).toBeVisible()
      expect((await page.getByTestId('initiative-detail-header').boundingBox())?.height).toBe(87.5)
      expect((await page.locator('.li-overview-icon').boundingBox())?.y).toBe(151.5)
      expect((await page.locator('.li-resources > h3').boundingBox())?.y).toBe(388)
      const latest = page.getByTestId('initiative-latest-update')
      await expect(latest).toContainText('[mobile] On schedule.')
      await expect(latest).toContainText('On track')
      await expect(latest).toHaveCSS('box-sizing', 'border-box')
      await expect(latest).toHaveCSS('border-top-style', 'solid')
      await expect(latest).toHaveCSS('box-shadow', 'none')
      expect((await latest.getByRole('button', { name: 'Comments', exact: true }).locator('svg').boundingBox())?.width).toBe(16)
      expect(await latest.getByRole('button', { name: 'Comments', exact: true }).evaluate(node => getComputedStyle(node, '::after').boxShadow)).not.toBe('none')
      expect(Math.abs((await latest.boundingBox())!.y - 471.5)).toBeLessThanOrEqual(1)
      await expect(page.locator('.li-detail-sidebar')).toHaveCount(0)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await latest.getByRole('button', { name: 'Update actions', exact: true }).click()
      await expect(page.getByRole('menuitem', { name: 'Copy link' })).toBeVisible()
      await page.keyboard.press('Escape')
      await latest.getByRole('button', { name: 'Comments', exact: true }).click()
      await expect(page).toHaveURL(/\/activity$/)
    })
  }
})

workspaceTest.describe('mobile initiatives list', () => {
  workspaceTest.use({ isMobile: true, hasTouch: true, colorScheme: 'dark' })

  for (const width of [390, 375]) {
    workspaceTest(`All initiatives keeps its phone header and on-track row at ${width}px`, async ({ page, request, workspaceKey }) => {
      const created = await request.post('/api/initiatives', {
        headers: { 'X-Flow-Workspace': workspaceKey, 'X-Workspace-Key': workspaceKey },
        data: { name: '[mobile] Initiative parity', status: 'active', health: 'onTrack' },
      })
      expect(created.status()).toBe(201)
      await page.setViewportSize({ width, height: width === 390 ? 844 : 667 })
      await page.goto(`/${workspaceKey}/initiatives`)
      await expect(page.locator('.li-toolbar nav a[aria-current=page]')).toHaveText('All initiatives')
      const header = page.getByTestId('initiatives-header')
      expect((await header.boundingBox())?.height).toBe(87.5)
      const row = page.locator('a.li-row')
      await expect(row).toContainText('[mobile] Initiative parity')
      expect((await row.boundingBox())?.y).toBe(87.5)
      expect((await row.boundingBox())?.height).toBe(56)
      expect((await row.boundingBox())?.width).toBe(width)
      expect((await row.locator('.li-initiative-icon').boundingBox())?.x).toBe(14)
      expect((await row.locator('.li-name strong').boundingBox())?.x).toBe(48)
      expect((await row.getByRole('button', { name: 'On track', exact: true }).boundingBox())?.x).toBe(width - 50)
      await expect(page.locator('.li-columns')).toBeHidden()
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)

      await page.getByRole('button', { name: 'Display options', exact: true }).click()
      await expect(page.getByText('Display properties', { exact: true })).toBeVisible()
      await page.keyboard.press('Escape')
      await page.locator('.li-toolbar-actions button[aria-label="Open sidebar"]').click()
      await expect(page.locator('.li-list-sidebar')).toBeVisible()
      await page.locator('.li-toolbar-actions button[aria-label="Close sidebar"]').click()
      await expect(page.locator('.li-list-sidebar')).toHaveCount(0)
      await page.getByRole('button', { name: 'New initiative', exact: true }).click()
      await expect(page.getByRole('textbox', { name: 'Initiative name', exact: true })).toBeVisible()
    })
  }
})

test.describe('mobile projects list', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('phone All projects rows show the icon, name, milestone, health and progress', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-projects-${testInfo.project.name}-${testInfo.retry}`
    const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: 'Mobile projects parity', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const created = await request.post('http://127.0.0.1:4180/api/projects', {
      data: { name: '[mobile] Delta', summary: 'Refresh the marketing site' },
      headers,
    })
    expect(created.status()).toBe(201)
    const project = await created.json() as { id: string }
    expect((await request.post(`http://127.0.0.1:4180/api/projects/${project.id}/milestones`, { data: { name: 'Alpha' }, headers })).status()).toBe(201)
    expect((await request.patch(`http://127.0.0.1:4180/api/projects/${project.id}`, { data: { statusId: 'ps_progress' }, headers })).ok()).toBe(true)

    const display = await request.put('http://127.0.0.1:4180/api/workspace/project-display-default', {
      headers,
      data: {
        display: {
          layout: 'list',
          grouping: 'No grouping',
          groupOrder: 'asc',
          subGrouping: 'No grouping',
          ordering: 'Name',
          orderingDirection: 'asc',
          showClosed: 'All',
          showEmptyGroups: false,
          properties: ['Milestones', 'Summary', 'Priority', 'Status', 'Health', 'Lead', 'Target date', 'Issues'],
          onlyLeadTeamProjects: false,
        },
      },
    })
    expect(display.ok()).toBe(true)

    await page.goto(`/${workspaceKey}/projects`)
    await expect(page.locator('[data-projects-header]')).toHaveCSS('height', '87.5px')
    const topBar = page.locator('.lp-projects__header')
    expect((await topBar.boundingBox())?.height).toBe(44)
    const menu = page.locator('.lp-projects__header [data-sidebar-trigger]')
    expect((await menu.boundingBox())?.width).toBe(32)
    expect((await page.locator('.lp-projects__header h1').boundingBox())?.x).toBe(52)
    expect((await page.locator('.lp-projects__header .lp-projects__new-project').boundingBox())?.x).toBe(350)

    const row = page.locator('a.lp-project-row').first()
    expect((await row.boundingBox())?.y).toBeCloseTo(87.5, 0)
    expect((await row.boundingBox())?.height).toBe(48)
    await expect(row).toContainText('[mobile] Delta')
    await expect(row.locator('.lp-project-row__milestone')).toContainText('Alpha')
    expect((await row.locator('.lp-project-row__health').boundingBox())?.width).toBe(32)
    expect((await row.locator('.lp-project-row__progress').boundingBox())?.width).toBe(28)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/projects-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })
})

test.describe('mobile project overview', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('phone project overview shows the header tabs, property pills and sections', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-projects-overview-${testInfo.project.name}-${testInfo.retry}`
    const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: 'Mobile project overview parity', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const created = await request.post('http://127.0.0.1:4180/api/projects', {
      headers,
      data: {
        name: '[mobile] Delta',
        summary: 'Pixel-level comparison fixture',
        description: '<p>This project tracks the mobile parity work for the overview screen.</p>',
        icon: 'Cube',
        statusId: 'ps_progress',
        priority: 2,
        startDate: '2026-09-28',
      },
    })
    expect(created.status()).toBe(201)
    const project = await created.json() as { id: string; slugId: string }
    const initiative = await request.post('http://127.0.0.1:4180/api/initiatives', {
      headers,
      data: { name: '[mobile] test123', summary: 'Mobile parity initiative', status: 'active' },
    })
    expect(initiative.status()).toBe(201)
    const initiativeId = (await initiative.json() as { id: string }).id
    expect((await request.patch(`http://127.0.0.1:4180/api/projects/${project.id}`, { headers, data: { initiatives: [initiativeId] } })).ok()).toBe(true)
    for (const [title, url] of [['Example', 'https://example.com'], ['GitHub', 'https://github.com/flow'], ['Docs', 'https://flow.example.com/docs']]) {
      expect((await request.post(`http://127.0.0.1:4180/api/projects/${project.id}/resources`, { headers, data: { type: 'link', title, url } })).status()).toBe(201)
    }

    await page.goto(`/${workspaceKey}/project/${project.slugId}/overview`)
    await page.locator('.project-detail-page__toolbar-actions button[aria-label="Close project details"]').click()

    const top = page.locator('.project-detail-page__top')
    expect((await top.boundingBox())?.height).toBe(87.5)
    expect(await top.evaluate(node => getComputedStyle(node).gap)).toBe('12px')
    expect((await page.locator('.project-detail-page__mobile-menu').boundingBox())?.width).toBe(32)
    expect((await page.locator('.project-detail-page__crumb').boundingBox())?.x).toBeCloseTo(52, 0)
    expect(Math.abs((await page.locator('.project-detail-page__header [aria-label*="favorites"]').boundingBox())!.x - 166.89)).toBeLessThanOrEqual(1)
    expect((await page.locator('.project-detail-page__toolbar-actions button[aria-label*="project details"]').boundingBox())?.x).toBe(350)
    expect((await page.locator('.project-detail-page__tab[aria-current="page"] > div').boundingBox())?.x).toBeCloseTo(18.5, 0)
    expect((await page.locator('.project-detail-page__tab[href$="activity"] > div').boundingBox())?.x).toBeCloseTo(97.95, 0)
    expect((await page.locator('.project-detail-page__add-view').boundingBox())?.x).toBeCloseTo(217.98, 0)

    const overview = page.locator('.project-overview')
    expect((await overview.boundingBox())?.x).toBe(40)
    expect((await overview.boundingBox())?.width).toBe(310)
    expect((await page.locator('.project-overview__name').boundingBox())?.y).toBeCloseTo(199.5, 0)
    expect((await page.locator('.project-overview__properties button[data-property="status"] > span').boundingBox())?.x).toBe(70)
    expect((await page.locator('.project-overview__properties button[data-property="priority"] > span').boundingBox())?.x).toBeCloseTo(180.27, 0)
    expect((await page.locator('.project-overview__row-section:has(.project-overview__initiatives) > h3').boundingBox())?.y).toBe(418)
    expect((await page.locator('.project-overview__resources > h3').boundingBox())?.y).toBe(497.5)
    expect((await page.locator('.project-customers-row__add').boundingBox())?.y).toBe(644.5)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/project-overview-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })
})

test.describe('mobile project properties panel', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('phone project details panel sits at the right edge over the overview with split date rows', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-projects-properties-${testInfo.project.name}-${testInfo.retry}`
    const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: 'Mobile project properties parity', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const created = await request.post('http://127.0.0.1:4180/api/projects', {
      headers,
      data: {
        name: '[mobile] Compare Test',
        summary: 'Properties panel parity fixture',
        description: '<p>This project checks the phone properties panel.</p>',
        statusId: 'ps_progress',
        priority: 2,
        startDate: '2026-09-28',
      },
    })
    expect(created.status()).toBe(201)
    const project = await created.json() as { id: string; slugId: string }
    const initiative = await request.post('http://127.0.0.1:4180/api/initiatives', {
      headers,
      data: { name: '[mobile] test123', summary: 'Properties panel initiative', status: 'active' },
    })
    expect(initiative.status()).toBe(201)
    const initiativeId = (await initiative.json() as { id: string }).id
    expect((await request.patch(`http://127.0.0.1:4180/api/projects/${project.id}`, { headers, data: { initiatives: [initiativeId] } })).ok()).toBe(true)
    expect((await request.post(`http://127.0.0.1:4180/api/projects/${project.id}/milestones`, { headers, data: { name: 'Alpha' } })).status()).toBe(201)
    expect((await request.post('http://127.0.0.1:4180/api/projects', { headers, data: { name: '[mobile] Companion' } })).status()).toBe(201)

    await page.goto(`/${workspaceKey}/project/${project.slugId}/overview`)

    const aside = page.locator('.project-details-sidebar')
    expect((await aside.boundingBox())?.x).toBe(40)
    expect((await aside.boundingBox())?.width).toBe(350)
    expect(await aside.evaluate(node => getComputedStyle(node).backgroundColor)).toBe('rgba(0, 0, 0, 0)')

    const properties = page.locator('.project-details-sidebar__section.is-compact')
    expect((await properties.boundingBox())?.x).toBe(44)
    expect((await properties.boundingBox())?.width).toBe(338)
    expect((await page.locator('.project-details-sidebar__property-label').first().boundingBox())?.x).toBe(57)
    expect((await page.locator('.project-details-sidebar__property-label').nth(4).textContent())).toBe('Start date')
    expect((await page.locator('.project-details-sidebar__property-label').nth(5).textContent())).toBe('Target date')
    expect((await page.locator('.project-details-sidebar__properties').boundingBox())?.height).toBe(352)
    expect((await page.locator('.project-details-sidebar__property .lp-project-property-trigger > span').first().boundingBox())?.x).toBe(177)
    expect((await page.locator('.project-details-sidebar__property-add').boundingBox())?.width).toBe(28)
    expect((await page.locator('.project-details-sidebar__section.is-milestones').boundingBox())?.y).toBeCloseTo(501, 0)
    expect((await page.locator('.project-details-sidebar__milestone').first().boundingBox())?.height).toBe(36)
    expect((await page.locator('.project-details-sidebar__section').last().boundingBox())?.y).toBeCloseTo(623.5, 0)
    expect((await page.locator('.project-details-sidebar__see-all > span').boundingBox())?.x).toBeCloseTo(331.5, 0)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/project-properties-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })
})

test.describe('mobile project activity', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('phone project activity shows the comment composer, a month heading and the framed comment card', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-projects-activity-${testInfo.project.name}-${testInfo.retry}`
    const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: 'Mobile project activity parity', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const created = await request.post('http://127.0.0.1:4180/api/projects', {
      headers,
      data: { name: 'Compare Test', summary: 'Activity parity fixture', statusId: 'ps_progress' },
    })
    expect(created.status()).toBe(201)
    const project = await created.json() as { id: string; slugId: string }
    const initiative = await request.post('http://127.0.0.1:4180/api/initiatives', {
      headers,
      data: { name: '[mobile] test123', summary: 'Activity parity initiative', status: 'active' },
    })
    expect(initiative.status()).toBe(201)
    const initiativeId = (await initiative.json() as { id: string }).id
    expect((await request.patch(`http://127.0.0.1:4180/api/projects/${project.id}`, { headers, data: { initiatives: [initiativeId] } })).ok()).toBe(true)
    expect((await request.post(`http://127.0.0.1:4180/api/projects/${project.id}/comments`, { headers, data: { body: 'Comparison comment for layout checks.' } })).status()).toBe(201)
    expect((await request.post(`http://127.0.0.1:4180/api/projects/${project.id}/milestones`, { headers, data: { name: '[mobile] Alpha' } })).status()).toBe(201)
    expect((await request.put(`http://127.0.0.1:4180/api/subscriptions/project/${project.id}`, { headers, data: { events: ['projectChanges'] } })).ok()).toBe(true)

    await page.goto(`/${workspaceKey}/project/${project.slugId}/activity`)
    await page.locator('.project-detail-page__toolbar-actions button[aria-label="Close project details"]').click()

    const near = (actual: number | undefined, expected: number) => actual !== undefined && Math.abs(actual - expected) <= 1
    expect(await page.locator('.project-detail-page__header button[aria-label="Setup project notifications"] path').getAttribute('d')).toContain('13.8076')
    const composer = page.locator('.project-activity__composer')
    expect((await composer.boundingBox())?.x).toBe(16)
    expect(near((await composer.boundingBox())?.y, 125.5)).toBe(true)
    expect((await composer.boundingBox())?.width).toBe(358)
    expect((await composer.boundingBox())?.height).toBe(176)
    expect(near((await page.locator('.project-activity__composer [role=tab][aria-selected="true"]').boundingBox())?.x, 29)).toBe(true)
    expect(near((await page.locator('.project-activity__composer [role=tab][aria-selected="true"]').boundingBox())?.y, 138.5)).toBe(true)
    expect(near((await page.locator('.project-activity__editor p').boundingBox())?.x, 32.5)).toBe(true)
    expect(near((await page.locator('.project-activity__editor p').boundingBox())?.y, 183)).toBe(true)
    expect((await page.locator('.project-activity__attach').boundingBox())?.width).toBe(32)
    expect(Math.abs((await page.locator('.project-activity__composer button.is-submit').boundingBox())!.x - 286.78)).toBeLessThanOrEqual(1)
    expect(near((await page.locator('.project-activity__composer button.is-submit').boundingBox())?.width, 72.72)).toBe(true)

    expect((await page.locator('.project-activity__event').first().boundingBox())?.y).toBe(313.5)
    expect(near((await page.locator('.project-activity__event strong').first().boundingBox())?.x, 44)).toBe(true)
    expect(near((await page.locator('.project-activity__month > span').boundingBox())?.y, 426.5)).toBe(true)
    const card = page.locator('.project-activity__comment-card')
    expect((await card.boundingBox())?.x).toBe(12)
    expect(near((await card.boundingBox())?.y, 474.5)).toBe(true)
    expect((await card.boundingBox())?.width).toBe(366)
    expect((await card.locator('.avatar').boundingBox())?.width).toBe(18)
    expect(near((await card.locator('.project-activity__comment-body p').boundingBox())?.y, 515)).toBe(true)
    expect((await card.locator('button[aria-label="Add reaction"]').boundingBox())?.width).toBe(32)
    expect((await card.locator('button[aria-label="Comment options"]').boundingBox())?.width).toBe(32)
    expect((await card.locator('.project-activity__thread-toggle').boundingBox())?.height).toBe(28)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/project-activity-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })
})

test.describe('mobile project update composer', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('phone project updates show the changed properties and post-update footer', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-projects-updates-${testInfo.project.name}-${testInfo.retry}`
    const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: 'Mobile project updates parity', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const created = await request.post('http://127.0.0.1:4180/api/projects', {
      headers,
      data: { name: 'Compare Test', summary: 'Update composer parity fixture' },
    })
    expect(created.status()).toBe(201)
    const project = await created.json() as { id: string; slugId: string }
    expect((await request.patch(`http://127.0.0.1:4180/api/projects/${project.id}`, { headers, data: { priority: 2 } })).ok()).toBe(true)
    expect((await request.patch(`http://127.0.0.1:4180/api/projects/${project.id}`, { headers, data: { startDate: '2026-09-28' } })).ok()).toBe(true)
    expect((await request.post(`http://127.0.0.1:4180/api/projects/${project.id}/comments`, { headers, data: { body: 'Comparison comment for layout checks.' } })).status()).toBe(201)

    await page.goto(`/${workspaceKey}/project/${project.slugId}/activity`)
    await page.locator('.project-detail-page__toolbar-actions button[aria-label="Close project details"]').click()
    await page.locator('.project-activity__composer div[role=tablist] button:nth-child(2)').click()

    const near = (actual: number | undefined, expected: number) => actual !== undefined && Math.abs(actual - expected) <= 1
    const composer = page.locator('.project-activity__composer')
    expect((await composer.boundingBox())?.height).toBe(257)
    expect((await composer.getAttribute('data-mode'))).toBe('update')
    expect(await page.locator('.project-activity__health').textContent()).toContain('On track')
    expect((await page.locator('.project-activity__health').boundingBox())?.height).toBe(28)
    expect(near((await page.locator('.project-activity__health > span').boundingBox())?.x, 193.27)).toBe(true)
    await expect(composer.locator('button.project-activity__agent')).toBeHidden()
    expect(near((await page.locator('.project-activity__diff-label').first().boundingBox())?.y, 262.75)).toBe(true)
    expect(near((await page.locator('.project-activity__diff-label').nth(1).boundingBox())?.y, 290)).toBe(true)
    expect(await page.locator('.project-activity__diff-row[data-field=priority]').textContent()).toContain('No priority')
    expect(await page.locator('.project-activity__diff-row[data-field=priority]').textContent()).toContain('High')
    expect(await page.locator('.project-activity__diff-row[data-field=startDate]').textContent()).toContain('Sep 28th')
    expect((await page.locator('.project-activity__attach').boundingBox())?.width).toBe(32)
    expect(near((await page.locator('.project-activity__attach').boundingBox())?.x, 229.99)).toBe(true)
    expect(near((await page.locator('.project-activity__attach').boundingBox())?.y, 338)).toBe(true)
    expect(near((await page.locator('.project-activity__composer button.is-submit').boundingBox())?.x, 273.99)).toBe(true)
    expect(near((await page.locator('.project-activity__composer button.is-submit').boundingBox())?.y, 340)).toBe(true)
    expect(near((await page.locator('.project-activity__month > span').boundingBox())?.y, 507.5)).toBe(true)
    expect(near((await page.locator('.project-activity__comment-card').boundingBox())?.y, 555.5)).toBe(true)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/project-updates-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })
})

test.describe('mobile project issues empty state', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('phone issues tab shows the centred empty state, shortcut keycaps and the display dot', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-projects-issues-${testInfo.project.name}-${testInfo.retry}`
    const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: 'Mobile project issues parity', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const created = await request.post('http://127.0.0.1:4180/api/projects', {
      headers,
      data: { name: 'Compare Test' },
    })
    expect(created.status()).toBe(201)
    const project = await created.json() as { id: string; slugId: string }

    await page.goto(`/${workspaceKey}/project/${project.slugId}/issues`)
    await page.locator('.project-detail-page__toolbar-actions button[aria-label="Close project details"]').click()
    await page.locator('button[aria-label="Display options"]').click()
    await page.locator('button[role="tab"]:has-text("Board")').click()
    await page.keyboard.press('Escape')

    const near = (actual: number | undefined, expected: number) => actual !== undefined && Math.abs(actual - expected) <= 1
    expect((await page.locator('.project-detail-page__top').boundingBox())?.height).toBe(87.5)
    expect((await page.locator('.project-detail-page__header').boundingBox())?.height).toBe(44)
    expect(near((await page.locator('.project-detail-page__tab[href$="issues"] > div').boundingBox())?.x, 166.55)).toBe(true)

    expect(near((await page.locator('button[aria-label="Add filter"]').boundingBox())?.x, 274)).toBe(true)
    expect(near((await page.locator('button[aria-label="Display options"]').boundingBox())?.x, 312)).toBe(true)
    expect((await page.locator('button[aria-label="Display options"]').boundingBox())?.width).toBe(32)
    expect(near((await page.locator('.project-detail-page__toolbar-actions button[aria-label*="project details"]').boundingBox())?.x, 350)).toBe(true)
    const dot = page.locator('.project-detail-page__toolbar-actions > button[aria-label="Display options"] span')
    expect((await dot.boundingBox())?.width).toBe(7)
    expect((await dot.boundingBox())?.height).toBe(7)

    const mark = page.locator('.project-issues__intro-mark-phone')
    expect((await mark.boundingBox())?.x).toBe(25)
    expect(near((await mark.boundingBox())?.y, 344.95)).toBe(true)
    expect((await mark.boundingBox())?.width).toBeCloseTo(64.86, 1)
    expect((await mark.boundingBox())?.height).toBe(60)
    expect(near((await page.locator('.project-issues__intro > strong').boundingBox())?.y, 428.95)).toBe(true)
    expect(near((await page.locator('.project-issues__intro > p').first().boundingBox())?.y, 459.95)).toBe(true)
    expect(near((await page.locator('.project-issues__intro > p').nth(1).boundingBox())?.y, 494.15)).toBe(true)
    const keycaps = page.locator('.project-issues__intro-hint kbd')
    expect(near((await keycaps.first().boundingBox())?.x, 226.48)).toBe(true)
    expect(near((await keycaps.nth(1).boundingBox())?.x, 247.48)).toBe(true)
    expect((await keycaps.first().boundingBox())?.width).toBe(18)
    expect((await keycaps.first().boundingBox())?.height).toBeCloseTo(17.1, 1)

    const create = page.locator('.project-issues__intro button')
    expect(near((await create.boundingBox())?.y, 554.54)).toBe(true)
    expect((await create.boundingBox())?.height).toBe(32)
    expect(near((await create.locator('kbd').boundingBox())?.x, 140.33)).toBe(true)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/project-issues-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })
})

test.describe('mobile project milestones section', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })
  const apiBase = process.env.FLOW_MOBILE_API_BASE ?? 'http://127.0.0.1:4180'

  test('phone overview scrolls to the description goals and milestone card', async ({ page, request }, testInfo) => {
    const requestedKey = `mobile-projects-milestones-${testInfo.project.name}-${Date.now()}`
    const response = await request.post(`${apiBase}/api/workspaces`, {
      data: { name: 'Mobile project milestones parity', urlKey: requestedKey, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const { workspace: { urlKey: workspaceKey } } = await response.json() as { workspace: { urlKey: string } }
    const headers = { 'X-Workspace-Key': workspaceKey }
    const created = await request.post(`${apiBase}/api/projects`, {
      headers,
      data: {
        name: 'Compare Test',
        summary: 'Pixel-level comparison fixture',
        description: '<p>This project is a fixture for comparing Flow with reference.</p><h2>Goals</h2><ul><li><p>Match layout</p></li><li><p>Match typography</p></li></ul>',
        icon: 'Cube',
        statusId: 'ps_progress',
        priority: 2,
        startDate: '2026-09-28',
      },
    })
    expect(created.status()).toBe(201)
    const project = await created.json() as { id: string; slugId: string }
    for (const [title, url] of [['123', 'https://example.com/123'], ['New document', 'https://example.com/new'], ['GitHub', 'https://github.com/flow'], ['Example', 'https://example.com']]) {
      expect((await request.post(`${apiBase}/api/projects/${project.id}/resources`, { headers, data: { type: 'link', title, url } })).status()).toBe(201)
    }
    expect((await request.post(`${apiBase}/api/projects/${project.id}/milestones`, { headers, data: { name: 'Alpha' } })).status()).toBe(201)

    await page.goto(`/${workspaceKey}/project/${project.slugId}/overview`)
    await page.locator('.project-detail-page__toolbar-actions button[aria-label="Close project details"]').click()
    await page.locator('.project-overview__milestones > h3').click()

    const near = (actual: number | undefined, expected: number) => actual !== undefined && Math.abs(actual - expected) <= 1
    expect(near((await page.locator('.project-overview__description > h3').boundingBox())?.y, 317.5)).toBe(true)
    expect(near((await page.locator('.project-overview__description-editor h2 > .project-description-text').boundingBox())?.y, 443.5)).toBe(true)
    expect(near((await page.locator('.project-overview__milestones > h3').boundingBox())?.y, 611)).toBe(true)
    expect((await page.locator('.project-overview__milestone').boundingBox())?.height).toBe(106)
    expect((await page.locator('.project-overview__milestone-date').boundingBox())?.width).toBe(28)
    expect((await page.locator('.project-overview__milestone-link').boundingBox())?.height).toBe(28)
    expect((await page.locator('.project-overview__latest').boundingBox())?.height).toBe(69)
    expect((await page.locator('.project-overview__first-update .project-overview__mobile-icon').boundingBox())?.width).toBe(16)
    const lines = await page.locator('.project-overview__description-editor li p').evaluateAll(nodes => nodes.map(node => {
      const rect = node.getBoundingClientRect(), style = getComputedStyle(node)
      return { y: rect.y, height: rect.height, overflow: style.overflow, transform: style.transform }
    }))
    expect(lines.every(line => line.height === 24 && line.overflow === 'visible' && line.transform === 'none')).toBe(true)
    expect(lines[1].y - lines[0].y).toBe(30)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/project-milestones-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })

  for (const width of [390, 375]) {
    test(`phone descriptions keep wrapped, ordered and nested list text unclipped at ${width}px`, async ({ page, request }, testInfo) => {
      const requestedKey = `mobile-projects-description-${width}-${testInfo.project.name}-${Date.now()}`
      const response = await request.post(`${apiBase}/api/workspaces`, {
        data: { name: '[mobile] Description lists', urlKey: requestedKey, region: 'us', tutorial: false },
      })
      expect(response.status()).toBe(201)
      const { workspace: { urlKey: workspaceKey } } = await response.json() as { workspace: { urlKey: string } }
      const headers = { 'X-Workspace-Key': workspaceKey }
      const created = await request.post(`${apiBase}/api/projects`, {
        headers,
        data: {
          name: '[mobile] Description lists',
          description: '<p>A long project description with typography and descenders that should wrap onto several lines without clipping any of its content.</p><h2>A heading long enough to wrap across several lines on a phone</h2><ul><li><p>Typography, grouping and layout stay fully visible when this list item wraps across multiple lines.</p><ul><li><p>Nested typography stays aligned.</p></li></ul></li><li><p>Second bullet typography.</p></li><li><p>Third bullet grouping.</p></li></ul><ol><li><p>Ordered typography also wraps across multiple lines without losing any descenders.</p></li><li><p>Another ordered paragraph.</p><p>A second paragraph within the same item.</p></li></ol>',
        },
      })
      expect(created.status()).toBe(201)
      const project = await created.json() as { slugId: string }
      await page.setViewportSize({ width, height: width === 390 ? 844 : 667 })
      await page.goto(`/${workspaceKey}/project/${project.slugId}/overview`)
      await page.locator('button[aria-label="Close project details"]').click()
      const blocks = await page.locator('.project-overview__description-editor .ProseMirror :is(p,h2)').evaluateAll(nodes => nodes.map(node => {
        const rect = node.getBoundingClientRect(), style = getComputedStyle(node)
        const range = document.createRange()
        range.selectNodeContents(node)
        return { text: node.textContent, height: rect.height, lineHeight: Number.parseFloat(style.lineHeight), overflow: style.overflow, transform: style.transform, textBottom: range.getBoundingClientRect().bottom - rect.top }
      }))
      expect(blocks.length).toBeGreaterThan(8)
      expect(blocks.filter(block => block.text).every(block => block.overflow === 'visible' && block.transform === 'none' && block.textBottom <= block.height)).toBe(true)
      expect(blocks.filter(block => block.height > block.lineHeight).length).toBeGreaterThanOrEqual(3)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    })
  }
})

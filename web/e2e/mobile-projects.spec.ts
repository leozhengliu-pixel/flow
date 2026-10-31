import { expect, test } from '@playwright/test'

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

  test('phone overview scrolls to the description goals and milestone card', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-projects-milestones-${testInfo.project.name}-${testInfo.retry}`
    const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: 'Mobile project milestones parity', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const created = await request.post('http://127.0.0.1:4180/api/projects', {
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
      expect((await request.post(`http://127.0.0.1:4180/api/projects/${project.id}/resources`, { headers, data: { type: 'link', title, url } })).status()).toBe(201)
    }
    expect((await request.post(`http://127.0.0.1:4180/api/projects/${project.id}/milestones`, { headers, data: { name: 'Alpha' } })).status()).toBe(201)

    await page.goto(`/${workspaceKey}/project/${project.slugId}/overview`)
    await page.locator('.project-detail-page__toolbar-actions button[aria-label="Close project details"]').click()
    await page.locator('.project-overview__milestones > h3').click()

    const near = (actual: number | undefined, expected: number) => actual !== undefined && Math.abs(actual - expected) <= 1
    expect(near((await page.locator('.project-overview__description > h3').boundingBox())?.y, 317.5)).toBe(true)
    expect(near((await page.locator('.project-overview__description-editor .ProseMirror > h2').boundingBox())?.y, 443.5)).toBe(true)
    expect(near((await page.locator('.project-overview__milestones > h3').boundingBox())?.y, 611)).toBe(true)
    expect((await page.locator('.project-overview__milestone').boundingBox())?.height).toBe(106)
    expect((await page.locator('.project-overview__milestone-date').boundingBox())?.width).toBe(28)
    expect((await page.locator('.project-overview__milestone-link').boundingBox())?.height).toBe(28)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/project-milestones-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })
})

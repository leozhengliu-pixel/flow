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

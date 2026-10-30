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

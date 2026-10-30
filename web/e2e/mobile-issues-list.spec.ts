import { expect, test } from '@playwright/test'

test.describe('mobile issue lists and views', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })

  test('keeps list, board, triage and views surfaces inside the phone viewport', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-issues-${testInfo.project.name}-${testInfo.retry}`
    const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: 'Mobile issue parity', urlKey: workspaceKey, region: 'us' },
    })
    expect(response.status()).toBe(201)
    const workspace = await response.json() as { teams?: Array<{ key: string }> }
    const teamKey = workspace.teams?.[0]?.key
    expect(teamKey).toBeTruthy()

    const routes = [
      `/${workspaceKey}/my-issues/assigned`,
      `/${workspaceKey}/issues/all`,
      `/${workspaceKey}/views/issues`,
      `/${workspaceKey}/team/${teamKey}/triage`,
    ]
    for (const route of routes) {
      await page.goto(route)
      await expect(page.locator('main').first()).toBeVisible()
      const geometry = await page.evaluate(() => ({
        width: innerWidth,
        height: innerHeight,
        scrollWidth: document.documentElement.scrollWidth,
        overflow: document.documentElement.scrollWidth <= innerWidth,
      }))
      expect(geometry.width).toBe(390)
      expect(geometry.height).toBe(844)
      expect(geometry.overflow, `${route} should not overflow horizontally`).toBe(true)
    }

    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    await page.getByRole('button', { name: 'Add filter', exact: true }).click()
    await expect(page.getByRole('dialog', { name: 'Add Filter…' })).toBeVisible()
    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/filter-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
    await page.keyboard.press('Escape')
    await page.getByRole('button', { name: 'Display options', exact: true }).click()
    await expect(page.getByRole('dialog', { name: 'Display options' })).toBeVisible()
    await page.keyboard.press('Escape')

    // My issues / Assigned: the reference tab strip keeps two views plus an overflow pill,
    // 32px pills, a 36px group header and 44px rows.
    const tabBar = page.locator('[data-content-view-subheader]')
    await expect(tabBar).toHaveCSS('height', '43.5px')
    const assignedTab = await page.getByRole('link', { name: /^Assigned/ }).boundingBox()
    expect(assignedTab?.height).toBe(32)
    const overflow = page.getByRole('button', { name: /more$/ })
    await expect(overflow).toBeVisible()
    expect((await overflow.boundingBox())?.width).toBeCloseTo(74.96, 1)
    await expect(page.locator('[data-my-issues-surface] [class*="groupHeader"]').first()).toHaveCSS('height', '36px')
    const row = page.locator('[data-my-issues-surface] a[data-issue-row-id]').first()
    expect((await row.boundingBox())?.height).toBe(44)
    await overflow.click()
    await expect(page.getByRole('menuitem', { name: 'Subscribed', exact: true })).toBeVisible()
    await page.keyboard.press('Escape')

    // The selected view moves into the active overflow pill when it does not fit.
    await page.goto(`/${workspaceKey}/my-issues/subscribed`)
    const activeOverflow = page.getByRole('button', { name: /^Subscribed & \d+ more$/ })
    await expect(activeOverflow).toHaveAttribute('data-active', 'true')
    await activeOverflow.click()
    const moreViews = page.getByRole('menu', { name: 'More views' })
    await expect(moreViews).toBeVisible()
    await expect(page.getByRole('menuitem').first()).toBeFocused()
    await page.keyboard.press('ArrowDown')
    await page.keyboard.press('Enter')
    await expect(moreViews).toBeHidden()

    await page.goto(`/${workspaceKey}/issues/all`)
    const display = page.getByRole('button', { name: 'Display options' }).first()
    await expect(display).toBeVisible()
    await display.click()
    const boardTab = page.getByRole('tab', { name: 'Board', exact: true })
    await expect(boardTab).toBeEnabled()
    await boardTab.click()
    await page.keyboard.press('Escape')
    const board = page.getByRole('list', { name: 'Issue board' })
    await expect(board).toBeVisible()
    await expect(board).toHaveCSS('overflow-x', 'auto')
  })
})

test.describe('mobile Created tab', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('phone Created tab is a flat list with the sub-issue parent trail', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-created-${testInfo.project.name}-${testInfo.retry}`
    const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: '[mobile] Created', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const bootstrap = await (await request.get('http://127.0.0.1:4180/api/bootstrap', { headers })).json() as { viewer: { id: string }; teams: Array<{ id: string }>; states: Array<{ id: string; type: string }> }
    const teamId = bootstrap.teams[0].id
    const stateId = bootstrap.states.find(state => state.type === 'unstarted')!.id
    const create = async (title: string, extra = {}) => {
      const created = await request.post('http://127.0.0.1:4180/api/issues', { headers, data: { title, description: '', teamId, stateId, ...extra } })
      expect(created.status()).toBe(201)
      return created.json() as Promise<{ id: string }>
    }
    const parent = await create('[mobile] Parent issue')
    await create('[mobile] Second issue')
    const child = await create('[mobile] Child #1', { parentId: parent.id, assigneeId: bootstrap.viewer.id })
    const reordered = await request.patch(`http://127.0.0.1:4180/api/issues/${child.id}`, { headers, data: { sortOrder: 0 } })
    expect(reordered.ok()).toBe(true)

    await page.goto(`/${workspaceKey}/my-issues/created`)
    await expect(page.locator('main').first()).toBeVisible()
    await page.getByRole('button', { name: 'Display options', exact: true }).click()
    await page.waitForTimeout(200)
    await page.locator('[role="dialog"][aria-label="Display options"] button[aria-label="Grouping"]').click()
    await page.waitForTimeout(200)
    await page.getByRole('option', { name: 'No grouping', exact: true }).click()
    await page.waitForTimeout(300)
    await page.keyboard.press('Escape')
    await expect(page.locator('[data-my-issues-surface] [class*="groupHeader"]').first()).toBeHidden()
    const row = page.locator('[data-my-issues-surface] a[data-issue-row-id]').first()
    expect((await row.boundingBox())?.y).toBeCloseTo(87.5, 0)
    expect((await row.boundingBox())?.height).toBe(44)
    await expect(row).toContainText('[mobile] Child #1')
    await expect(row.locator('[class*="parentTrail"]')).toContainText('[mobile] Parent issue')
    await expect(row.locator('[data-row-avatar]')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
})

import { expect, test } from '@playwright/test'
import { test as workspaceTest } from './workspace-fixture'
import type { BootstrapData } from '../src/types/flow'

workspaceTest.describe('mobile issue lists and views', () => {
  workspaceTest.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })

  workspaceTest('keeps list, triage and views surfaces inside the phone viewport', async ({ page, request, workspaceKey }) => {
    const response = await request.get('/api/bootstrap', { headers: { 'X-Workspace-Key': workspaceKey } })
    expect(response.ok()).toBeTruthy()
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
  })

  workspaceTest('keeps filter, display and overflow views usable on the phone', async ({ page, request, workspaceKey }, testInfo) => {
    const headers = { 'X-Workspace-Key': workspaceKey }
    const bootstrap = await request.get('/api/bootstrap', { headers })
    expect(bootstrap.ok()).toBeTruthy()
    const data = await bootstrap.json() as BootstrapData
    const issue = await request.post('/api/issues', { headers, data: { title: 'Assigned phone issue', teamId: data.teams[0].id, assigneeId: data.viewer.id } })
    expect(issue.status()).toBe(201)
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
    const moreViewsMenu = page.getByRole('menu', { name: 'More views' })
    await expect(moreViewsMenu).toBeVisible()
    await expect(moreViewsMenu.getByRole('menuitem')).toHaveCount(3)
    await expect(moreViewsMenu.getByRole('menuitem', { name: 'Subscribed', exact: true })).toBeVisible()
    await expect(moreViewsMenu.getByRole('menuitem', { name: 'Activity', exact: true })).toBeVisible()
    await expect(moreViewsMenu.getByRole('menuitem', { name: 'Shared with me', exact: true })).toBeVisible()
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
  })

  workspaceTest('switches the workspace issue list to a horizontally scrollable phone board', async ({ page, workspaceKey }) => {
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

test.describe('mobile team display options', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('opens the compact team display sheet without phone overflow', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-team-display-${testInfo.project.name}-${testInfo.retry}`
    const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: '[mobile] Team display options', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const bootstrap = await (await request.get('http://127.0.0.1:4180/api/bootstrap', { headers })).json() as { teams: Array<{ id: string; key: string }> }
    const team = bootstrap.teams[0]
    const child = await request.post(`http://127.0.0.1:4180/api/workspaces/${workspaceKey}/teams`, {
      headers,
      data: { name: '[mobile] Display sub-team', key: 'SUB', parentTeamId: team.id },
    })
    expect(child.status()).toBe(201)

    await page.goto(`/${workspaceKey}/team/${team.key}/all`)
    await page.getByRole('button', { name: 'Display options', exact: true }).click()
    const panel = page.getByRole('dialog', { name: 'Display options' })
    await expect(panel).toBeVisible()
    await expect(panel.getByRole('tab', { name: 'List', exact: true })).toHaveAttribute('aria-selected', 'true')
    await expect(panel.locator('input[aria-label="Show sub-team issues"]')).toBeChecked()
    await expect(panel.locator('button[aria-label="Nested sub-issues"]')).toContainText('Show matching')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
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

test.describe('mobile Subscribed tab', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('phone Subscribed tab nests a sub-issue under its parent with an indent guide', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-subscribed-${testInfo.project.name}-${testInfo.retry}`
    const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: '[mobile] Subscribed', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const bootstrap = await (await request.get('http://127.0.0.1:4180/api/bootstrap', { headers })).json() as {
      viewer: { id: string }
      teams: Array<{ id: string }>
      states: Array<{ id: string; type: string }>
      labels: Array<{ id: string }>
    }
    const teamId = bootstrap.teams[0].id
    const stateId = bootstrap.states.find(state => state.type === 'unstarted')!.id
    const settings = await request.patch(`http://127.0.0.1:4180/api/teams/${teamId}/settings`, { headers, data: { estimateType: 'fibonacci' } })
    expect(settings.ok()).toBe(true)
    const create = async (title: string, extra = {}) => {
      const created = await request.post('http://127.0.0.1:4180/api/issues', { headers, data: { title, description: '', teamId, stateId, ...extra } })
      expect(created.status()).toBe(201)
      return created.json() as Promise<{ id: string }>
    }
    const parent = await create('[mobile] Parent issue')
    const child = await create('[mobile] Nested child one', { parentId: parent.id })
    const other = await create('[mobile] Label badge issue')
    const subscribed = [bootstrap.viewer.id]
    await request.patch(`http://127.0.0.1:4180/api/issues/${parent.id}`, { headers, data: { subscriberIds: subscribed, estimate: 3, sortOrder: 0 } })
    await request.patch(`http://127.0.0.1:4180/api/issues/${child.id}`, { headers, data: { subscriberIds: subscribed, sortOrder: 1 } })
    await request.patch(`http://127.0.0.1:4180/api/issues/${other.id}`, { headers, data: { subscriberIds: subscribed, labelIds: bootstrap.labels[0] ? [bootstrap.labels[0].id] : [], sortOrder: 2 } })

    await page.goto(`/${workspaceKey}/my-issues/subscribed`)
    await expect(page.locator('main').first()).toBeVisible()
    await page.getByRole('button', { name: 'Display options', exact: true }).click()
    await page.waitForTimeout(200)
    await page.locator('[role="dialog"][aria-label="Display options"] button[aria-label="Grouping"]').click()
    await page.waitForTimeout(200)
    await page.getByRole('option', { name: 'No grouping', exact: true }).click()
    await page.waitForTimeout(200)
    await page.getByRole('checkbox', { name: 'Nested sub-issues' }).check()
    await page.waitForTimeout(200)
    await page.keyboard.press('Escape')
    await expect(page.locator('[data-my-issues-surface] [class*="groupHeader"]').first()).toBeHidden()
    const surface = page.locator('[data-my-issues-surface]')
    const row = surface.locator('a[data-issue-row-id]').first()
    expect((await row.boundingBox())?.y).toBeCloseTo(87.5, 0)
    expect((await row.boundingBox())?.height).toBe(44)
    await expect(row).toContainText('[mobile] Parent issue')
    const nested = surface.locator('a[data-issue-row-id][data-nested="true"]').first()
    await expect(nested).toContainText('[mobile] Nested child one')
    expect((await nested.boundingBox())?.y).toBeCloseTo(131.5, 0)
    const guide = surface.locator('[class*="nestingGuide"]').first()
    await expect(guide).toBeVisible()
    expect((await guide.boundingBox())?.x).toBeCloseTo(16, 0)
    expect((await guide.boundingBox())?.width).toBeCloseTo(24, 0)
    await expect(row.locator('.estimate-chip__value')).toContainText('3')
    await expect(page.getByRole('button', { name: /Subscribed/ })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
})

test.describe('mobile Activity tab', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('phone Activity tab groups issues by relative activity day', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-activity-${testInfo.project.name}-${testInfo.retry}`
    const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: '[mobile] Activity', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const bootstrap = await (await request.get('http://127.0.0.1:4180/api/bootstrap', { headers })).json() as { viewer: { id: string }; teams: Array<{ id: string }>; states: Array<{ id: string; type: string }> }
    const teamId = bootstrap.teams[0].id
    const stateId = bootstrap.states.find(state => state.type === 'unstarted')!.id
    const create = async (title: string) => {
      const created = await request.post('http://127.0.0.1:4180/api/issues', { headers, data: { title, description: '', teamId, stateId, assigneeId: bootstrap.viewer.id } })
      expect(created.status()).toBe(201)
      return created.json() as Promise<{ id: string }>
    }
    await create('[mobile] Activity issue one')
    await create('[mobile] Activity issue two')

    await page.goto(`/${workspaceKey}/my-issues/activity`)
    await expect(page.locator('main').first()).toBeVisible()
    await page.getByRole('button', { name: 'Display options', exact: true }).click()
    await page.waitForTimeout(200)
    const displayPanel = page.getByRole('dialog', { name: 'Display options' })
    await expect(displayPanel).toBeVisible()
    expect((await displayPanel.boundingBox())?.width).toBeCloseTo(301, 0)
    expect((await displayPanel.boundingBox())?.height).toBeCloseTo(478, 0)
    await expect(displayPanel.getByRole('tab', { name: 'List', exact: true })).toHaveAttribute('aria-selected', 'true')
    await expect(displayPanel.locator('input[aria-label="Show sub-issues"]')).toBeChecked()
    await expect(displayPanel.locator('input[aria-label="Show triage issues"]')).toBeChecked()
    await expect(displayPanel.locator('button[aria-label="Nested sub-issues"]')).toContainText('Show matching')
    await page.keyboard.press('Escape')
    await page.getByRole('button', { name: 'Display options', exact: true }).click()
    await page.waitForTimeout(200)
    await page.locator('[role="dialog"][aria-label="Display options"] button[aria-label="Grouping"]').click()
    await page.waitForTimeout(200)
    await page.getByRole('option', { name: 'My activity', exact: true }).click()
    await page.waitForTimeout(300)
    await page.keyboard.press('Escape')
    const surface = page.locator('[data-my-issues-surface]')
    const header = surface.locator('[class*="groupHeader"]').first()
    await expect(header).toBeVisible()
    await expect(header.locator('[class*="groupName"]')).toHaveText('Today')
    await expect(header.locator('[class*="groupCount"]')).toHaveText('2')
    await expect(header.getByRole('button', { name: 'Create new issue' })).toHaveCount(0)
    expect((await header.boundingBox())?.height).toBe(36)
    const row = surface.locator('a[data-issue-row-id]').first()
    expect((await row.boundingBox())?.y).toBeCloseTo(125.5, 0)
    expect((await row.boundingBox())?.height).toBe(44)
    await expect(row).toContainText('[mobile] Activity issue')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
})

test.describe('desktop my issues tab strip', () => {
  test.use({ viewport: { width: 1440, height: 900 }, isMobile: false, hasTouch: false })

  test('shows only the five tab links without the off-screen measurement row', async ({ page, request }, testInfo) => {
    const workspaceKey = `desktop-issues-${testInfo.project.name}-${testInfo.retry}`
    const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: 'Desktop issue parity', urlKey: workspaceKey, region: 'us' },
    })
    expect(response.status()).toBe(201)
    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    const nav = page.getByRole('navigation', { name: 'My issues views' })
    await expect(nav).toBeVisible()
    await expect(nav.getByRole('link')).toHaveCount(5)
    await expect(page.locator('[data-tab-measure]')).toHaveCount(0)
    await expect(page.locator('[class*="tabMeasure"]')).toHaveCount(0)
    const labels = await nav.getByRole('link').evaluateAll(nodes => nodes.map(node => (node.textContent ?? '').trim()))
    expect(labels).toEqual(['Assigned', 'Created', 'Subscribed', 'Activity', 'Shared with me'])
    const visibleText = await nav.evaluate(node => (node as HTMLElement).innerText.replace(/\s+/g, ' ').trim())
    expect(visibleText).toBe('Assigned Created Subscribed Activity Shared with me')
    const names = await nav.getByRole('link').evaluateAll(nodes => nodes.map(node => node.getAttribute('aria-label') ?? (node.textContent ?? '').trim()))
    expect(new Set(names).size).toBe(5)
  })
})

test.describe('mobile Assigned details panel', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('opens the right aligned details drawer over a scrim with four summary tabs', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-assigned-details-${testInfo.project.name}-${testInfo.retry}`
    const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: '[mobile] Assigned details', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const bootstrap = await (await request.get('http://127.0.0.1:4180/api/bootstrap', { headers })).json() as { viewer: { id: string }; teams: Array<{ id: string }>; states: Array<{ id: string; type: string }> }
    const teamId = bootstrap.teams[0].id
    const stateId = bootstrap.states.find(state => state.type === 'unstarted')!.id
    for (const title of ['[mobile] Assigned detail one', '[mobile] Assigned detail two']) {
      const created = await request.post('http://127.0.0.1:4180/api/issues', { headers, data: { title, description: '', teamId, stateId, assigneeId: bootstrap.viewer.id } })
      expect(created.status()).toBe(201)
    }

    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    await expect(page.locator('main').first()).toBeVisible()
    await page.getByRole('button', { name: 'Open details', exact: true }).click()
    await page.waitForTimeout(500)

    const scrim = page.locator('[data-testid="my-issues-details-scrim"]')
    await expect(scrim).toBeVisible()
    expect((await scrim.boundingBox())?.y).toBeCloseTo(87.5, 0)
    const drawer = page.locator('[data-testid="my-issues-details-drawer"]')
    await expect(drawer).toBeVisible()
    const viewport = await page.evaluate(() => ({ width: innerWidth, height: innerHeight }))
    const drawerBox = await drawer.boundingBox()
    expect(drawerBox?.x).toBeCloseTo(viewport.width - 350, 0)
    expect(drawerBox?.width).toBeCloseTo(350, 0)
    const panel = page.locator('[data-testid="my-issues-details-panel"]')
    const panelBox = await panel.boundingBox()
    expect(panelBox?.x).toBeCloseTo(viewport.width - 346, 0)
    expect(panelBox?.width).toBeCloseTo(338, 0)
    expect(panelBox?.height).toBeCloseTo(viewport.height - 95.5, 0)
    await expect(panel).toHaveCSS('border-radius', '10px')
    const tabs = panel.getByRole('tab')
    await expect(tabs).toHaveCount(4)
    await expect(tabs).toHaveText(['Labels', 'Priority', 'Projects', 'Teams'])
    await expect(panel.getByRole('tabpanel')).toContainText('No labels used')

    await page.getByRole('tab', { name: 'Teams', exact: true }).click()
    await expect(page.getByRole('tab', { name: 'Teams', exact: true })).toHaveAttribute('aria-selected', 'true')
    await expect(panel.getByRole('tabpanel')).toHaveAttribute('aria-label', 'Teams summary')
    await page.getByRole('tab', { name: 'Labels', exact: true }).click()
    await expect(panel.getByRole('tabpanel')).toContainText('No labels used')

    await scrim.click({ position: { x: 10, y: 20 } })
    await expect(drawer).toBeHidden()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
})

test.describe('mobile team issues list', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('phone team header, estimate-only group and nested sub-issue match the reference', async ({ page, request }, testInfo) => {
    const apiBase = process.env.FLOW_E2E_API_URL ?? 'http://127.0.0.1:4180'
    const workspaceKey = `mobile-team-list-${testInfo.project.name}-${testInfo.retry}-${Date.now()}`
    const response = await request.post(`${apiBase}/api/workspaces`, {
      data: { name: '[mobile] Team list', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const bootstrap = await (await request.get(`${apiBase}/api/bootstrap`, { headers })).json() as {
      teams: Array<{ id: string; key: string }>
      states: Array<{ id: string; type: string }>
    }
    const team = bootstrap.teams[0]
    const stateId = bootstrap.states.find(state => state.type === 'unstarted')!.id
    const renamed = await request.patch(`${apiBase}/api/workspaces/${workspaceKey}/teams/${team.id}`, {
      headers,
      data: { name: 'Picker Test', icon: 'StarOutline', color: '#E719A5' },
    })
    expect(renamed.ok()).toBe(true)
    const settings = await request.patch(`${apiBase}/api/teams/${team.id}/settings`, {
      headers,
      data: {
        estimateType: 'fibonacci',
        issueViewDefaults: {
          all: {
            layout: 'list', grouping: 'status', groupOrder: 'asc', subGrouping: 'none', ordering: 'created',
            orderCompletedByRecency: false, showSubIssues: true, showEmptyGroups: false, nestedSubIssues: true,
            hiddenGroupIds: [], properties: ['priority', 'status', 'estimate', 'assignee'],
          },
        },
      },
    })
    expect(settings.ok()).toBe(true)
    const create = async (title: string, extra = {}) => {
      const created = await request.post(`${apiBase}/api/issues`, {
        headers,
        data: { title, description: '', teamId: team.id, stateId, priority: 0, ...extra },
      })
      expect(created.status()).toBe(201)
      return created.json() as Promise<{ id: string }>
    }
    const parentA = await create('Estimate test A', { estimate: 3 })
    await create('Estimate test B', { estimate: 5 })
    await create('Nested child one', { estimate: 1, parentId: parentA.id })

    await page.goto(`/${workspaceKey}/team/${team.key}/all`)
    await expect(page.locator('main').first()).toBeVisible()
    const header = page.locator('[data-content-view-header]')
    await expect(header.getByRole('switch', { name: 'Add to favorites' })).toBeVisible()
    await expect(header.getByRole('link', { name: 'Setup team notifications' })).toBeVisible()
    await expect(header.getByRole('button', { name: 'New issue' })).toBeVisible()
    const headerGeometry = await page.evaluate(() => {
      const star = document.querySelector('[data-content-view-header] button[aria-label="Add to favorites"]')!.getBoundingClientRect()
      const bell = document.querySelector('[data-content-view-header] [aria-label="Setup team notifications"]')!.getBoundingClientRect()
      const create = document.querySelector('[data-content-view-header] .phone-create-issue')!.getBoundingClientRect()
      return { starX: star.x, starSize: star.width, bellX: bell.x, createX: create.x }
    })
    expect(headerGeometry.starX).toBeCloseTo(209.97, 0)
    expect(headerGeometry.starSize).toBe(32)
    expect(headerGeometry.bellX).toBeCloseTo(314, 0)
    expect(headerGeometry.createX).toBeCloseTo(350, 0)

    const toolbar = page.locator('[data-content-view-subheader]')
    await expect(toolbar.getByRole('button', { name: 'Open details' })).toBeVisible()
    await expect(toolbar.getByRole('button', { name: 'Display options' })).toBeVisible()
    const groupHeader = page.locator('[data-issue-explorer] [class*="groupHeader"]').first()
    await expect(groupHeader).toHaveCSS('height', '36px')
    await expect(groupHeader.locator('[class*="groupCount"]')).toBeHidden()
    await expect(groupHeader.locator('.estimate-chip__value')).toHaveText('9')
    const rows = page.locator('[data-issue-explorer] a[data-issue-row-id]')
    await expect(rows).toHaveCount(3)
    await expect(rows.nth(0)).toContainText('Estimate test B')
    await expect(rows.nth(1)).toContainText('Estimate test A')
    const nested = page.locator('[data-issue-explorer] a[data-issue-row-id][data-nested="true"]')
    await expect(nested).toContainText('Nested child one')
    expect((await nested.boundingBox())?.y).toBeCloseTo(213.5, 0)
    await expect(nested.locator('.estimate-chip')).toBeHidden()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
})

test.describe('mobile Team issues board', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('phone board shows a 298px Todo column with the hidden columns column peeking in', async ({ page, request }, testInfo) => {
    const apiBase = process.env.FLOW_E2E_API_URL ?? 'http://127.0.0.1:4180'
    const response = await request.post(`${apiBase}/api/workspaces`, {
      data: { name: '[mobile] Team board', urlKey: `mobile-team-board-${testInfo.project.name}-${testInfo.retry}-${Date.now()}`, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const { workspace } = await response.json() as { workspace: { urlKey: string } }
    const workspaceKey = workspace.urlKey
    const headers = { 'X-Workspace-Key': workspaceKey }
    const bootstrap = await (await request.get(`${apiBase}/api/bootstrap`, { headers })).json() as {
      teams: Array<{ id: string; key: string }>
      states: Array<{ id: string; type: string }>
    }
    const team = bootstrap.teams[0]
    const stateId = bootstrap.states.find(state => state.type === 'unstarted')!.id
    const settings = await request.patch(`${apiBase}/api/teams/${team.id}/settings`, { headers, data: { estimateType: 'fibonacci' } })
    expect(settings.ok()).toBe(true)
    const create = async (title: string, extra = {}) => {
      const created = await request.post(`${apiBase}/api/issues`, {
        headers,
        data: { title, description: '', teamId: team.id, stateId, priority: 0, ...extra },
      })
      expect(created.status()).toBe(201)
      return created.json() as Promise<{ id: string }>
    }
    const parent = await create('[mobile] Estimate test A', { estimate: 3 })
    await create('[mobile] Estimate test B', { estimate: 5 })
    await create('[mobile] Nested child one', { estimate: 1, parentId: parent.id })

    await page.goto(`/${workspaceKey}/team/${team.key}/all`)
    await expect(page.locator('main').first()).toBeVisible()
    await page.getByRole('button', { name: 'Display options' }).first().click()
    await page.getByRole('tab', { name: 'Board', exact: true }).click()
    const emptyColumns = page.locator('input[aria-label="Show empty columns"]')
    if (!(await emptyColumns.isChecked())) await emptyColumns.click()
    for (const name of ['Backlog', 'In Progress', 'Done', 'Canceled', 'Duplicate']) {
      await page.getByRole('button', { name: `Hide group ${name}`, exact: true }).click()
    }
    await page.keyboard.press('Escape')

    const board = page.getByRole('list', { name: 'Issue board' })
    await expect(board).toBeVisible()
    const column = board.locator('[data-board-column]').first()
    expect((await column.boundingBox())?.width).toBe(298)
    const columnHeader = column.locator('[data-board-column-header]')
    await expect(columnHeader).toHaveCSS('height', '50px')
    await expect(columnHeader).toHaveCSS('position', 'sticky')
    const card = column.locator('[data-board-card]').first()
    const cardBox = await card.boundingBox()
    expect(cardBox?.x).toBe(17)
    expect(cardBox?.width).toBe(264)
    const addIssue = await column.locator('[data-board-add-issue]').boundingBox()
    expect(addIssue?.x).toBe(18)
    expect(addIssue?.width).toBe(262)
    expect(addIssue?.height).toBe(28)
    expect((await board.locator('[data-board-hidden-header]').boundingBox())?.x).toBe(299)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
})

test.describe('mobile Team issues board scrolled to the hidden columns', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('phone board scrolls the hidden statuses column into view', async ({ page, request }, testInfo) => {
    const apiBase = process.env.FLOW_E2E_API_URL ?? 'http://127.0.0.1:4180'
    const workspaceKey = `mobile-scrolled-${testInfo.project.name}-${testInfo.retry}-${Date.now()}`
    const response = await request.post(`${apiBase}/api/workspaces`, {
      data: { name: '[mobile] Team board scrolled', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(response.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const created = await response.json() as { teams: Array<{ id: string; key: string }> }
    const team = created.teams[0]
    const review = await request.post(`${apiBase}/api/teams/${team.id}/states`, {
      headers,
      data: { name: 'In Review', type: 'started', color: '#2ECC71', position: 2.5 },
    })
    expect(review.status()).toBe(201)
    const statesResponse = await request.get(`${apiBase}/api/teams/${team.id}/states`, { headers })
    expect(statesResponse.ok()).toBe(true)
    const states = await statesResponse.json() as Array<{ id: string; type: string }>
    const stateId = states.find(state => state.type === 'unstarted')!.id
    const renamed = await request.patch(`${apiBase}/api/workspaces/${workspaceKey}/teams/${team.id}`, {
      headers,
      data: { name: '[mobile] UI', icon: 'StarOutline', color: '#E719A5' },
    })
    expect(renamed.ok()).toBe(true)
    const settings = await request.patch(`${apiBase}/api/teams/${team.id}/settings`, { headers, data: { estimateType: 'fibonacci' } })
    expect(settings.ok()).toBe(true)
    const create = async (title: string, extra = {}) => {
      const created = await request.post(`${apiBase}/api/issues`, {
        headers,
        data: { title, description: '', teamId: team.id, stateId, priority: 0, ...extra },
      })
      expect(created.status()).toBe(201)
      return created.json() as Promise<{ id: string }>
    }
    const parent = await create('[mobile] Estimate test A', { estimate: 3 })
    await create('[mobile] Estimate test B', { estimate: 5 })
    await create('[mobile] Nested child one', { estimate: 1, parentId: parent.id })

    await page.goto(`/${workspaceKey}/team/${team.key}/all#hidden-columns`)
    await expect(page.locator('main').first()).toBeVisible()
    await page.getByRole('button', { name: 'Display options' }).first().click()
    await page.getByRole('tab', { name: 'Board', exact: true }).click()
    const emptyColumns = page.locator('input[aria-label="Show empty columns"]')
    if (!(await emptyColumns.isChecked())) await emptyColumns.click()
    for (const name of ['Backlog', 'In Progress', 'In Review', 'Done', 'Canceled', 'Duplicate']) {
      await page.getByRole('button', { name: `Hide group ${name}`, exact: true }).click()
    }
    await page.keyboard.press('Escape')

    const board = page.getByRole('list', { name: 'Issue board' })
    await expect(board).toBeVisible()
    expect(await board.evaluate(node => node.scrollLeft)).toBe(258)
    const header = board.locator('[data-board-hidden-header-strip]')
    const headerBox = await header.boundingBox()
    expect(headerBox?.x).toBe(40)
    expect(headerBox?.width).toBe(338)
    expect(headerBox?.height).toBe(50)
    await expect(header).toHaveCSS('position', 'sticky')
    const collapse = board.locator('[data-board-hidden-header]')
    expect((await collapse.boundingBox())?.x).toBe(41)
    const rows = board.locator('[data-board-hidden-status]')
    await expect(rows).toHaveCount(6)
    const rowBox = await rows.first().boundingBox()
    expect(rowBox?.x).toBe(40)
    expect(rowBox?.width).toBe(338)
    expect(rowBox?.height).toBe(37)
    await expect(rows.first()).toContainText('Backlog')
    const name = rows.first().locator('div > span')
    const nameBox = await name.boundingBox()
    expect(Math.abs((nameBox?.x ?? 0) - 80.5)).toBeLessThanOrEqual(1)
    await expect(name).toHaveCSS('font-size', '13px')
    await expect(name).toHaveCSS('line-height', '18.2px')
    const count = rows.first().locator('div > b')
    const countBox = await count.boundingBox()
    expect(Math.abs((countBox?.x ?? 0) - 340.07)).toBeLessThanOrEqual(1)
    expect(Math.abs((countBox?.width ?? 0) - 25.43)).toBeLessThanOrEqual(1)
    await expect(count.locator('.estimate-icon')).toBeHidden()
    await expect(count.locator('svg:not(.estimate-icon)')).toBeVisible()
    if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/phone-hidden-board.png`, animations: 'disabled' })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
})
